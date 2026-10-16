package dandan

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
	"github.com/kzw200015/danfuse/backend/internal/naming"
)

const (
	maxSeasons    = 50 // search/episodes、search/anime 最多返回的季数，match 最多给出的候选数
	minKeywordLen = 2  // 协议规则：关键词至少 2 个字符
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
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
func typeOf(s Season) (animeType, description string) {
	kind := kinds[s.Kind]
	if s.Year == nil {
		return kind.animeType, kind.name
	}
	return kind.animeType, fmt.Sprintf("%s · %d", kind.name, *s.Year)
}

// episodeTitle "第N话 {集标题}"，没有集标题时只写"第N话"。search/episodes 没有集号字段，
// 插件从首集标题的"第N话"解析起始集号；bangumi 也用同样的标题，两条路径列出的集一致。
func episodeTitle(e Episode) string {
	if e.Title == "" {
		return fmt.Sprintf("第%d话", e.Number)
	}
	return fmt.Sprintf("第%d话 %s", e.Number, e.Title)
}
