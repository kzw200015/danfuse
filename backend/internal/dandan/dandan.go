// Package dandan 弹弹 API：jellyfin-danmaku 插件把 danfuse 当作弹弹play 服务器，按弹弹play 协议的语义调用。
// 这里只做协议参数的转换和响应的格式化（type、typeDescription、"第N话"、p、cid、平台前缀），搜索与取弹幕交给 provider 的聚合层。
//
// 它是统一响应约定的例外：响应按官方 Swagger 的结构输出，不用 response 包。handler 自己把错误转成弹弹play 的
// 结构返回 500，不交给全局 errorHandler，并按 errorHandler 的字段记一条日志；前缀下路由不匹配的 404、405 仍走全局处理。
// 插件不发、也不能加鉴权头，X-App* 这类请求头一律忽略。
package dandan

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/logger"
	"github.com/kzw200015/danfuse/backend/internal/provider"
)

const (
	maxSeasons    = 50 // search/episodes 最多返回的季数
	minKeywordLen = 2  // 协议规则：关键词至少 2 个字符
)

type Handler struct {
	provider provider.Provider // 聚合层
}

func NewHandler(p provider.Provider) *Handler {
	return &Handler{provider: p}
}

// responseBase 弹弹play 除弹幕列表外各接口响应的公共字段（ResponseBase）。
type responseBase struct {
	ErrorCode    int     `json:"errorCode"`
	Success      bool    `json:"success"`
	ErrorMessage *string `json:"errorMessage"`
	ErrorDetail  *string `json:"errorDetail"`
}

var (
	succeeded = responseBase{Success: true}
	failed    = responseBase{ErrorCode: 1, ErrorMessage: new("服务器内部错误")} // 沿用官方示例里的错误码与提示
)

// searchEpisodesResponse 官方 Swagger 的 SearchEpisodesResponse。
type searchEpisodesResponse struct {
	responseBase
	HasMore bool    `json:"hasMore"`
	Animes  []anime `json:"animes"` // 插件不判空，任何时候都输出 []
}

// anime 官方的 SearchEpisodesAnime：一个作品，对应一季。
type anime struct {
	AnimeID         int64     `json:"animeId"`
	AnimeTitle      string    `json:"animeTitle"`
	Type            string    `json:"type"`
	TypeDescription string    `json:"typeDescription"`
	Episodes        []episode `json:"episodes"`
}

// episode 官方的 SearchEpisodeDetails。
type episode struct {
	EpisodeID    int64  `json:"episodeId"`
	EpisodeTitle string `json:"episodeTitle"`
}

// SearchEpisodes GET search/episodes?anime=
// anime 按表单编码的规则解析（插件不对关键词做 URL 编码，标题里的 & # + 会把它截断或变形，不做补救），
// 去掉首尾空白后不足 2 个字符直接返回空列表；最多返回 50 季，被截断时 hasMore 为 true。episode、tmdbId 忽略。
// 插件自动匹配时取 animes[0]，再按数组下标取集。
func (h *Handler) SearchEpisodes(c *echo.Context) error {
	resp := searchEpisodesResponse{responseBase: succeeded, Animes: []anime{}}
	// 表单编码里只有 & 是分隔符，; 是普通字符（例如 Steins;Gate）；Go 的 url.ParseQuery 出于自己的安全取舍
	// 会丢掉含 ; 的整个参数，所以先把它转义。解析出错的参数照样丢掉，与 URL.Query 一致
	query, _ := url.ParseQuery(strings.ReplaceAll(c.Request().URL.RawQuery, ";", "%3B"))
	keyword := strings.TrimSpace(query.Get("anime"))
	if utf8.RuneCountInString(keyword) < minKeywordLen {
		return c.JSON(http.StatusOK, resp)
	}

	result, err := h.provider.Search(c.Request().Context(), provider.SearchQuery{Keyword: keyword, MaxSeasons: maxSeasons})
	if err != nil {
		return serverError(c, err, searchEpisodesResponse{responseBase: failed, Animes: []anime{}})
	}
	resp.HasMore = result.HasMore
	for _, s := range result.Seasons {
		resp.Animes = append(resp.Animes, toAnime(s))
	}
	return c.JSON(http.StatusOK, resp)
}

// commentResponse 官方的 CommentResponseV2：只有 count 和 comments，不带 ResponseBase。
type commentResponse struct {
	Count    int       `json:"count"`
	Comments []comment `json:"comments"` // 插件读不到这个字段时按出错处理，任何时候都输出 []
}

// comment 官方的 CommentData。插件只读 p 和 m。
type comment struct {
	CID int64  `json:"cid"`
	P   string `json:"p"`
	M   string `json:"m"`
}

// Comment GET comment/:episodeId
// 这一集所有绑定合并后的全部弹幕原文，按校正后时间升序。withRelated、from、chConvert 忽略。
// 不存在的 episodeId（包括不是整数的）、没有绑定的集返回空列表。
func (h *Handler) Comment(c *echo.Context) error {
	resp := commentResponse{Comments: []comment{}}
	episodeID, err := strconv.ParseInt(c.Param("episodeId"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusOK, resp)
	}

	items, err := h.provider.Comments(c.Request().Context(), episodeID)
	if err != nil {
		return serverError(c, err, resp) // 官方没有为 comment 定义错误响应，响应体沿用它的 schema
	}
	resp.Count = len(items)
	resp.Comments = make([]comment, len(items))
	for i, it := range items {
		resp.Comments[i] = toComment(it)
	}
	return c.JSON(http.StatusOK, resp)
}

// relatedResponse related 的响应：ResponseBase 加 relateds。
type relatedResponse struct {
	responseBase
	Relateds []struct{} `json:"relateds"`
}

// Related GET related/:episodeId
// 官方已下线这个接口，插件仍会在每次取弹幕后调用。固定返回空的 relateds，插件也就不会再去调 extcomment。
func (h *Handler) Related(c *echo.Context) error {
	return c.JSON(http.StatusOK, relatedResponse{responseBase: succeeded, Relateds: []struct{}{}})
}

// serverError 服务端故障：HTTP 500 加弹弹play 结构的响应体（列表为空）。错误不交给全局 errorHandler，
// 所以在这里按它的字段记日志。
func serverError(c *echo.Context, err error, body any) error {
	logger.ServerError(c, err)
	return c.JSON(http.StatusInternalServerError, body)
}
