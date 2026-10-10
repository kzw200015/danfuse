package server

import (
	"crypto/subtle"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/dandan"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding"
)

// Handlers 汇总各领域的 handler，供路由注册使用。
type Handlers struct {
	Health        *HealthHandler
	Settings      *SettingsHandler
	Catalog       *catalog.Handler
	Sync          *catalog.SyncHandler
	Binding       *binding.Handler
	SeasonBinding *seasonbinding.Handler
	Dandan        *dandan.Handler
}

func registerRoutes(e *echo.Echo, h *Handlers) {
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
	api.GET("/bindings/:id/danmaku", h.Binding.ListDanmaku)
	api.POST("/episodes/:id/file-bindings", h.Binding.CreateFromFiles)
	api.POST("/bindings/:id/files", h.Binding.AppendFiles)
	api.GET("/bindings/:id/files", h.Binding.ListFiles)
	api.POST("/bindings/:id/reparse", h.Binding.Reparse)

	api.POST("/seasons/:id/season-bindings/preview", h.SeasonBinding.Preview)
	api.POST("/seasons/:id/season-bindings", h.SeasonBinding.Create)
	api.GET("/season-bindings/:id", h.SeasonBinding.Get)
	api.PATCH("/season-bindings/:id", h.SeasonBinding.Update)
	api.POST("/season-bindings/:id/backfill", h.SeasonBinding.Backfill)
	api.DELETE("/season-bindings/:id", h.SeasonBinding.Delete)
	api.GET("/episode-rules/default", h.SeasonBinding.DefaultEpisodeRule)

	api.POST("/sync-runs", h.Sync.Trigger)
	api.GET("/sync-runs", h.Sync.List)
	api.GET("/sync-runs/latest", h.Sync.Latest)
	api.GET("/sync-runs/:id", h.Sync.Get)

	api.GET("/settings", h.Settings.Get)
}

// registerDandanRoutes 弹弹 API 挂在 /dandanplay[/<token>]/api/v2 下，客户端在填写的地址后面固定拼 /api/v2。
// CORS（浏览器里的客户端跨域请求，例如插件从 Jellyfin 的源请求）与 Gzip 只作用于这个前缀，管理 API 不加。
// 配置了 token 时前缀注册为 /dandanplay/:token/api/v2，由组中间件比较 token：5xx 日志记录的是注册时的路由模式，
// token 因此不会出现在日志里。只注册从搜索到取弹幕的接口，其余（match/batch、extcomment、search/tmdb 等）返回 404。
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
