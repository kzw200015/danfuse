package handler

import (
	"github.com/labstack/echo/v5"
)

// Register 把屏蔽词的增删挂到管理 API 的组（/api）下。
func (h *Handler) Register(api *echo.Group) {
	api.GET("/blocked-words", h.List)
	api.POST("/blocked-words", h.Create)
	api.DELETE("/blocked-words/:id", h.Delete)
}
