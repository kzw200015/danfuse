// Package dandan 弹弹 API：播放器（例如 jellyfin-danmaku 插件）把 danfuse 当作弹弹play 服务器，按弹弹play 协议的语义调用。
// 实现了从识别、搜索到取弹幕的接口：match 按文件名识别到集；search/episodes 一步搜到季和集，
// search/anime 搜到季再用 bangumi 取集；comment 取弹幕。
// 这里只做协议参数的转换和响应的格式化（type、typeDescription、"第N话"、p、cid、平台前缀），
// 识别、搜索、取季与取弹幕交给 provider 的聚合层；名称里的季号、集号怎么认，见 catalog.ParseName。
//
// 它是统一响应约定的例外：响应按官方 Swagger 的结构输出，不用 response 包。handler 自己把错误转成弹弹play 的
// 结构返回 500，不交给全局 errorHandler，并按 errorHandler 的字段记一条日志；前缀下路由不匹配的 404、405 仍走全局处理。
// 官方的鉴权头（X-AppId、X-Signature 这类）一律忽略，访问控制只靠路径里的 token。
package dandan

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/logger"
	"github.com/kzw200015/danfuse/backend/internal/provider"
)

const (
	maxSeasons    = 50 // search/episodes、search/anime 最多返回的季数，match 最多给出的候选数
	minKeywordLen = 2  // 协议规则：关键词至少 2 个字符
)

type Handler struct {
	provider *provider.Aggregator
}

func NewHandler(p *provider.Aggregator) *Handler {
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
	// 官方说明找不到作品时"返回资源未找到错误"，但没有公布错误码；用 404 与服务器内部错误的 1 区分
	animeNotFound = responseBase{ErrorCode: 404, ErrorMessage: new("作品不存在")}
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

// SearchEpisodes GET search/episodes?anime=&episode=
// anime 的规则见 searchKeyword，其中写明的季号、集号只要这一季、这一集（catalog.ParseName）。episode 为正整数时
// 只保留这一集，优先于 anime 里写明的集号；其他值（包括 C1、S1、O1 这样的格式）忽略。没有给 episode 时，
// 协议把 anime 里空格后面的数字当作集号，这里不这样做：没有标注的数字分不出是标题的一部分、季号还是集号。
// 最多返回 50 季，被截断时 hasMore 为 true。tmdbId 忽略。插件自动匹配时取 animes[0]，再按数组下标取集。
func (h *Handler) SearchEpisodes(c *echo.Context) error {
	resp := searchEpisodesResponse{responseBase: succeeded, Animes: []anime{}}
	query := formQuery(c)
	keyword, ok := searchKeyword(query, "anime")
	if !ok {
		return c.JSON(http.StatusOK, resp)
	}
	var episode *int
	if n, err := strconv.Atoi(query.Get("episode")); err == nil && n > 0 {
		episode = &n
	}

	result, err := h.provider.Search(c.Request().Context(), provider.SearchQuery{Keyword: keyword, MaxSeasons: maxSeasons, Episode: episode})
	if err != nil {
		return serverError(c, err, searchEpisodesResponse{responseBase: failed, Animes: []anime{}})
	}
	resp.HasMore = result.HasMore
	for _, s := range result.Seasons {
		resp.Animes = append(resp.Animes, toAnime(s))
	}
	return c.JSON(http.StatusOK, resp)
}

// searchAnimeResponse 官方的 SearchAnimeResponse：没有 hasMore。
type searchAnimeResponse struct {
	responseBase
	Animes []searchAnime `json:"animes"`
}

// searchAnime 官方的 SearchAnimeDetails：一个作品（一季），不带集。目录里没有的信息按 Swagger 的类型输出：
// 海报不经弹弹 API 提供、目录只有年份不编造上映日期，所以 imageUrl、startDate 为 null；没有评分和用户，rating 为 0、isFavorited 为 false。
type searchAnime struct {
	AnimeID         int64      `json:"animeId"`
	BangumiID       string     `json:"bangumiId"`
	AnimeTitle      string     `json:"animeTitle"`
	Type            string     `json:"type"`
	TypeDescription string     `json:"typeDescription"`
	ImageURL        *string    `json:"imageUrl"`
	StartDate       *time.Time `json:"startDate"`
	EpisodeCount    int        `json:"episodeCount"`
	Rating          float64    `json:"rating"`
	IsFavorited     bool       `json:"isFavorited"`
}

// SearchAnime GET search/anime?keyword=
// 与 search/episodes 是同一个搜索，keyword 的规则也相同（见 searchKeyword）；空格分开的词都要命中，与协议的说明一致。
// 结果是季，不带集，只给总集数，客户端再用 bangumiId 取作品详情。协议的响应没有 hasMore，最多返回 50 季，多出的截掉。
// type、v2 忽略。
func (h *Handler) SearchAnime(c *echo.Context) error {
	resp := searchAnimeResponse{responseBase: succeeded, Animes: []searchAnime{}}
	keyword, ok := searchKeyword(formQuery(c), "keyword")
	if !ok {
		return c.JSON(http.StatusOK, resp)
	}

	result, err := h.provider.Search(c.Request().Context(), provider.SearchQuery{Keyword: keyword, MaxSeasons: maxSeasons})
	if err != nil {
		return serverError(c, err, searchAnimeResponse{responseBase: failed, Animes: []searchAnime{}})
	}
	for _, s := range result.Seasons {
		resp.Animes = append(resp.Animes, toSearchAnime(s))
	}
	return c.JSON(http.StatusOK, resp)
}

// formQuery 按表单编码的规则解析查询串：有的客户端（例如 jellyfin-danmaku 插件）不对关键词做 URL 编码，
// 标题里的 & # + 会把它截断或变形，不做补救。
func formQuery(c *echo.Context) url.Values {
	// 表单编码里只有 & 是分隔符，; 是普通字符（例如 Steins;Gate）；Go 的 url.ParseQuery 出于自己的安全取舍
	// 会丢掉含 ; 的整个参数，所以先把它转义。解析出错的参数照样丢掉，与 URL.Query 一致
	query, _ := url.ParseQuery(strings.ReplaceAll(c.Request().URL.RawQuery, ";", "%3B"))
	return query
}

// searchKeyword 取参数 name 作为搜索关键词，去掉首尾空白；不足 2 个字符时 ok 为 false，调用方直接返回空列表（协议规则）。
func searchKeyword(query url.Values, name string) (keyword string, ok bool) {
	keyword = strings.TrimSpace(query.Get(name))
	return keyword, utf8.RuneCountInString(keyword) >= minKeywordLen
}

// bangumiResponse 官方的 BangumiDetailsResponse：作品不存在时 bangumi 为 null。
type bangumiResponse struct {
	responseBase
	Bangumi *bangumiDetails `json:"bangumi"`
}

// bangumiDetails 官方的 BangumiDetails，字段一个不少，按完整 schema 解码的客户端也能读。目录里没有的信息
// （海报、简介、评分、标签、关联作品等）按 Swagger 的类型输出零值：不可空的为 false、0，列表为 []，ratingDetails 为 {}，其余为 null。
type bangumiDetails struct {
	AnimeID         int64              `json:"animeId"`
	BangumiID       string             `json:"bangumiId"`
	AnimeTitle      string             `json:"animeTitle"`
	ImageURL        *string            `json:"imageUrl"`
	SearchKeyword   *string            `json:"searchKeyword"`
	IsOnAir         bool               `json:"isOnAir"`
	AirDay          int                `json:"airDay"`
	IsFavorited     bool               `json:"isFavorited"`
	IsRestricted    bool               `json:"isRestricted"`
	Rating          float64            `json:"rating"`
	Type            string             `json:"type"`
	TypeDescription string             `json:"typeDescription"`
	Titles          []struct{}         `json:"titles"`
	Seasons         []struct{}         `json:"seasons"`
	Episodes        []bangumiEpisode   `json:"episodes"`
	Summary         *string            `json:"summary"`
	Intro           *string            `json:"intro"`
	Metadata        []string           `json:"metadata"`
	BangumiURL      *string            `json:"bangumiUrl"`
	UserRating      int                `json:"userRating"`
	FavoriteStatus  *string            `json:"favoriteStatus"`
	Comment         *string            `json:"comment"`
	RatingDetails   map[string]float64 `json:"ratingDetails"`
	Relateds        []struct{}         `json:"relateds"`
	Similars        []struct{}         `json:"similars"`
	Tags            []struct{}         `json:"tags"`
	OnlineDatabases []struct{}         `json:"onlineDatabases"`
	Trailers        []struct{}         `json:"trailers"`
}

// bangumiEpisode 官方的 BangumiEpisode。一个作品就是一季，seasonId 为 null（协议：为空表示只有一季）；
// 目录里没有放送日期和观看记录，airDate、lastWatched 为 null。
type bangumiEpisode struct {
	SeasonID      *string    `json:"seasonId"`
	EpisodeID     int64      `json:"episodeId"`
	EpisodeTitle  string     `json:"episodeTitle"`
	EpisodeNumber string     `json:"episodeNumber"`
	LastWatched   *time.Time `json:"lastWatched"`
	AirDate       *time.Time `json:"airDate"`
}

// Bangumi GET bangumi/:bangumiId
// 作品详情：bangumiId 按季 ID 解析（search/anime 给出的 bangumiId、animeId 都可以），返回这一季和它的全部集。
// 不是整数、不在本地号段内或季不存在时，按协议的"资源未找到"返回：HTTP 200、success 为 false、bangumi 为 null。
func (h *Handler) Bangumi(c *echo.Context) error {
	notFound := bangumiResponse{responseBase: animeNotFound}
	id, err := strconv.ParseInt(c.Param("bangumiId"), 10, 64)
	if err != nil {
		return c.JSON(http.StatusOK, notFound)
	}

	season, found, err := h.provider.Season(c.Request().Context(), id)
	if err != nil {
		return serverError(c, err, bangumiResponse{responseBase: failed})
	}
	if !found {
		return c.JSON(http.StatusOK, notFound)
	}
	return c.JSON(http.StatusOK, bangumiResponse{responseBase: succeeded, Bangumi: toBangumi(season)})
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
