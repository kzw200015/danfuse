// Package handler 屏蔽词的管理 API：只绑定与校验参数、调用 service、输出响应。
package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/blockword/service"
)

// Handler 屏蔽词的管理 API，接口在 blocked_word.go。
type Handler struct {
	svc *service.Service
}

func NewHandler(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// Register 把屏蔽词的增删挂到管理 API 的组（/api）下。
func (h *Handler) Register(api *echo.Group) {
	api.GET("/blocked-words", h.List)
	api.POST("/blocked-words", h.Create)
	api.DELETE("/blocked-words/:id", h.Delete)
}
