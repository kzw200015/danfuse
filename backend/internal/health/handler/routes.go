package handler

import (
	"github.com/labstack/echo/v5"
)

// Register 把健康检查挂到管理 API 的组（/api）下。
func (h *Handler) Register(api *echo.Group) {
	api.GET("/health", h.Check)
}
