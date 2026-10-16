// Package handler 只读的设置（GET /api/settings）：从运行中的配置里挑出管理界面要用的部分。
// 没有业务逻辑，直接读配置，不设 service。
package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/config"
)

// Handler 拿整份配置：多返回一项配置时只改 Get，不改构造函数；不该返回的（API key、SESSDATA 的值）由 Get 自己挑出去。
type Handler struct {
	cfg *config.Config
}

func NewHandler(cfg *config.Config) *Handler {
	return &Handler{cfg: cfg}
}

// Register 把只读的设置挂到管理 API 的组（/api）下。
func (h *Handler) Register(api *echo.Group) {
	api.GET("/settings", h.Get)
}
