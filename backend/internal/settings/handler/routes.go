package handler

import (
	"github.com/labstack/echo/v5"
)

// Register 把只读的设置挂到管理 API 的组（/api）下。
func (h *Handler) Register(api *echo.Group) {
	api.GET("/settings", h.Get)
}
