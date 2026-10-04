package server

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/handler"
)

func registerRoutes(e *echo.Echo, h *handler.Handlers) {
	api := e.Group("/api")

	api.GET("/health", h.Health.Check)

	api.GET("/series", h.Catalog.ListSeries)
	api.GET("/series/:id", h.Catalog.GetSeries)
	api.GET("/images/:id", h.Catalog.GetImage)

	api.POST("/episodes/:id/bindings", h.Binding.Create)

	api.POST("/sync-runs", h.Sync.Trigger)
	api.GET("/sync-runs", h.Sync.List)
	api.GET("/sync-runs/:id", h.Sync.Get)

	api.GET("/settings", h.Settings.Get)
}
