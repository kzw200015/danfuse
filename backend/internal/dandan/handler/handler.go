// Package handler 弹弹 API：播放器（例如 jellyfin-danmaku 插件）把 danfuse 当作弹弹play 服务器，按弹弹play 协议的语义调用。
// 实现了从识别、搜索到取弹幕的接口：match 按文件名识别到集；search/episodes 一步搜到季和集，
// search/anime 搜到季再用 bangumi 取集；comment 取弹幕。
// Handler 只做协议参数的转换和响应的格式化（type、typeDescription、"第N话"、p、cid、平台前缀），
// 识别、搜索、取季与取弹幕交给同级 service 包的 Service；名称里的季号、集号怎么认，见 naming.Parse。
// 每个接口一个文件：请求与响应的结构、转换成协议结构的函数和 handler 都在接口同名的文件里（search.go、bangumi.go、comment.go、match.go），
// 各接口共用的（响应的公共字段、季的类型、日志、错误应答）和 related 在 handler.go。
//
// 它是统一响应约定的例外：响应按官方 Swagger 的结构输出，不用 response 包的统一响应。handler 自己把错误转成弹弹play 的
// 结构返回 500，不交给全局 errorHandler，并用 response.LogServerError 按同样的字段记一条日志；
// 前缀下路由不匹配的 404、405 仍走全局处理。
// 官方的鉴权头（X-AppId、X-Signature 这类）一律忽略，访问控制只靠路径里的 token。
//
// 除 related 外，每次请求成功应答后记一条 info 日志（logRequest），用来评估识别、搜索的效果和取弹幕的情况。
package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/dandan/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
	"github.com/kzw200015/danfuse/backend/internal/naming"
)

const (
	maxSeasons    = 50 // search/episodes、search/anime 最多返回的季数，match 最多给出的候选数
	minKeywordLen = 2  // 协议规则：关键词至少 2 个字符
)

type Handler struct {
	svc *service.Service
	cfg config.Dandanplay // 路径里的 token，见 Register
}

func NewHandler(svc *service.Service, cfg config.Dandanplay) *Handler {
	return &Handler{svc: svc, cfg: cfg}
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

// logRequest 弹弹 API 一次成功应答的 info 日志：request_id（与 5xx 日志的字段相同）、处理耗时，加上各接口自己的字段。
func logRequest(c *echo.Context, start time.Time, msg string, attrs ...slog.Attr) {
	attrs = append([]slog.Attr{
		slog.String("request_id", c.Response().Header().Get(echo.HeaderXRequestID)),
		slog.Duration("duration", time.Since(start)),
	}, attrs...)
	c.Logger().LogAttrs(c.Request().Context(), slog.LevelInfo, msg, attrs...)
}

// optionalInt v 为 nil 时返回空的 Attr，日志里不出现这个字段。
func optionalInt(key string, v *int) slog.Attr {
	if v == nil {
		return slog.Attr{}
	}
	return slog.Int(key, *v)
}

// serverError 服务端故障：HTTP 500 加弹弹play 结构的响应体（列表为空）。错误不交给全局 errorHandler，
// 所以在这里按它的字段记日志。
func serverError(c *echo.Context, err error, body any) error {
	response.LogServerError(c, err)
	return c.JSON(http.StatusInternalServerError, body)
}

// kinds 季的类别在协议里的 type（AnimeType 枚举）和 typeDescription 里的类型名。
var kinds = map[naming.SeasonKind]struct{ animeType, name string }{
	naming.KindSeries:  {"tvseries", "剧集"},
	naming.KindSpecial: {"tvspecial", "特别篇"},
	naming.KindMovie:   {"movie", "电影"},
}

// typeOf 季在协议里的 type 和 typeDescription：typeDescription 是类型名，有年份时加上年份，如"剧集 · 2011"。
func typeOf(s service.Season) (animeType, description string) {
	kind := kinds[s.Kind]
	if s.Year == nil {
		return kind.animeType, kind.name
	}
	return kind.animeType, fmt.Sprintf("%s · %d", kind.name, *s.Year)
}

// episodeTitle "第N话 {集标题}"，没有集标题时只写"第N话"。search/episodes 没有集号字段，
// 插件从首集标题的"第N话"解析起始集号；bangumi 也用同样的标题，两条路径列出的集一致。
func episodeTitle(e service.Episode) string {
	if e.Title == "" {
		return fmt.Sprintf("第%d话", e.Number)
	}
	return fmt.Sprintf("第%d话 %s", e.Number, e.Title)
}
