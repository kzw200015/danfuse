package handler

import (
	"crypto/subtle"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
)

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
