package server

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/handler"
)

func registerRoutes(e *echo.Echo, h *handler.Handlers) {
	api := e.Group("/api")

	api.GET("/health", h.Health.Check)

	users := api.Group("/users")
	users.GET("", h.User.List)
	users.POST("", h.User.Create)
	users.GET("/:id", h.User.Get)
}
