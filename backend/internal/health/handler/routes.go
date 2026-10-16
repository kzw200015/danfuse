// Package handler 健康检查的 API。
package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/health/service"
)

type Handler struct {
	svc *service.Service
}

func NewHandler(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// Register 把健康检查挂到管理 API 的组（/api）下。
func (h *Handler) Register(api *echo.Group) {
	api.GET("/health", h.Check)
}
