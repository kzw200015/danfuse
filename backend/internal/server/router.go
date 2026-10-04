package server

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/dandan"
	"github.com/kzw200015/danfuse/backend/internal/handler"
)

func registerRoutes(e *echo.Echo, h *handler.Handlers) {
	api := e.Group("/api")

	api.GET("/health", h.Health.Check)

	api.GET("/series", h.Catalog.ListSeries)
	api.GET("/series/:id", h.Catalog.GetSeries)
	api.DELETE("/series/:id", h.Catalog.DeleteSeries)
	api.DELETE("/seasons/:id", h.Catalog.DeleteSeason)
	api.DELETE("/episodes/:id", h.Catalog.DeleteEpisode)
	api.GET("/images/:id", h.Catalog.GetImage)

	api.POST("/episodes/:id/bindings", h.Binding.Create)
	api.PATCH("/bindings/:id", h.Binding.Update)
	api.DELETE("/bindings/:id", h.Binding.Delete)
	api.POST("/bindings/:id/refetch", h.Binding.Refetch)

	api.POST("/sync-runs", h.Sync.Trigger)
	api.GET("/sync-runs", h.Sync.List)
	api.GET("/sync-runs/:id", h.Sync.Get)

	api.GET("/settings", h.Settings.Get)
}

// registerDandanRoutes 弹弹 API 挂在 /dandanplay[/<token>]/api/v2 下，插件在填写的地址后面固定拼 /api/v2。
// CORS（插件在浏览器里从 Jellyfin 的源跨域请求）与 Gzip 只作用于这个前缀，管理 API 不加。
// 配置了 token 时前缀注册为 /dandanplay/:token/api/v2，由组中间件比较 token：5xx 日志记录的是注册时的路由模式，
// token 因此不会出现在日志里。extcomment、bangumi、match、search/anime 不注册，返回 404。
func registerDandanRoutes(e *echo.Echo, cfg config.Dandanplay, h *dandan.Handler) {
	prefix := "/dandanplay/api/v2"
	var middlewares []echo.MiddlewareFunc
	if cfg.Token != "" {
		prefix = "/dandanplay/:token/api/v2"
		middlewares = append(middlewares, checkToken(cfg.Token))
	}
	middlewares = append(middlewares,
		// 组上的中间件对组内没命中的路由同样生效（Echo 为组注册了 404 路由），OPTIONS 预检因此也经过 CORS
		middleware.CORSWithConfig(middleware.CORSConfig{
			AllowOrigins: []string{"*"},
			AllowMethods: []string{http.MethodGet, http.MethodOptions},
			MaxAge:       86400,
			// AllowHeaders 留空：预检时回显请求里的 Access-Control-Request-Headers
		}),
		middleware.Gzip(),
	)

	g := e.Group(prefix, middlewares...)
	g.GET("/search/episodes", h.SearchEpisodes)
	g.GET("/comment/:episodeId", h.Comment)
	g.GET("/related/:episodeId", h.Related)
}
