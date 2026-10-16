// Package handler 弹弹 API：播放器（例如 jellyfin-danmaku 插件）把 danfuse 当作弹弹play 服务器，按弹弹play 协议的语义调用。
// 实现了从识别、搜索到取弹幕的接口：match 按文件名识别到集；search/episodes 一步搜到季和集，
// search/anime 搜到季再用 bangumi 取集；comment 取弹幕。
// Handler 只做协议参数的转换和响应的格式化（type、typeDescription、"第N话"、p、cid、平台前缀），
// 识别、搜索、取季与取弹幕交给同级 service 包的 Service；名称里的季号、集号怎么认，见 naming.Parse。
// 每个接口一个文件：请求与响应的结构、转换成协议结构的函数和 handler 都在接口同名的文件里（search.go、bangumi.go、comment.go、
// match.go、related.go），各接口共用的协议部分（响应的公共字段、季的类型、日志、错误应答）在 protocol.go。
//
// 它是统一响应约定的例外：响应按官方 Swagger 的结构输出，不用 response 包的统一响应。handler 自己把错误转成弹弹play 的
// 结构返回 500，不交给全局 errorHandler，并用 response.LogServerError 按同样的字段记一条日志；
// 前缀下路由不匹配的 404、405 仍走全局处理。
// 官方的鉴权头（X-AppId、X-Signature 这类）一律忽略，访问控制只靠路径里的 token。
//
// 除 related 外，每次请求成功应答后记一条 info 日志（logRequest），用来评估识别、搜索的效果和取弹幕的情况。
package handler

import (
	"crypto/subtle"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/kzw200015/danfuse/backend/internal/dandan/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/config"
)

type Handler struct {
	svc *service.Service
	cfg config.Dandanplay // 路径里的 token，见 Register
}

func NewHandler(svc *service.Service, cfg config.Dandanplay) *Handler {
	return &Handler{svc: svc, cfg: cfg}
}

// Register 弹弹 API 挂在 /dandanplay[/<token>]/api/v2 下，客户端在填写的地址后面固定拼 /api/v2。
// CORS（浏览器里的客户端跨域请求，例如插件从 Jellyfin 的源请求）与 Gzip 只作用于这个前缀，管理 API 不加。
// 配置了 token 时前缀注册为 /dandanplay/:token/api/v2，由组中间件比较 token：5xx 日志记录的是注册时的路由模式，
// token 因此不会出现在日志里。只注册从搜索到取弹幕的接口，其余（match/batch、extcomment、search/tmdb 等）返回 404。
func (h *Handler) Register(e *echo.Echo) {
	prefix := "/dandanplay/api/v2"
	var middlewares []echo.MiddlewareFunc
	if h.cfg.Token != "" {
		prefix = "/dandanplay/:token/api/v2"
		middlewares = append(middlewares, checkToken(h.cfg.Token))
	}
	middlewares = append(middlewares,
		// 组上的中间件对组内没命中的路由同样生效（Echo 为组注册了 404 路由），OPTIONS 预检因此也经过 CORS
		middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins: []string{"*"},
			AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions}, // POST 用于 match
			MaxAge:       86400,
			// AllowHeaders 留空：预检时回显请求里的 Access-Control-Request-Headers
		}),
		middleware.Gzip(),
	)

	g := e.Group(prefix, middlewares...)
	g.POST("/match", h.Match)
	g.GET("/search/episodes", h.SearchEpisodes)
	g.GET("/search/anime", h.SearchAnime)
	g.GET("/bangumi/:bangumiId", h.Bangumi)
	g.GET("/comment/:episodeId", h.Comment)
	g.GET("/related/:episodeId", h.Related)
}

// checkToken 弹弹 API 路径里的 token 不对时按路由不存在处理（404，交给全局 errorHandler）。
// 常数时间比较，不从响应时间泄露 token。
func checkToken(token string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if subtle.ConstantTimeCompare([]byte(c.Param("token")), []byte(token)) != 1 {
				return echo.ErrNotFound
			}
			return next(c)
		}
	}
}
