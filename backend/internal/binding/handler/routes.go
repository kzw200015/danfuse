package handler

import (
	"github.com/labstack/echo/v5"
)

// Register 把绑定（贴链接、弹幕文件、查看弹幕）挂到管理 API 的组（/api）下。
func (h *Handler) Register(api *echo.Group) {
	api.POST("/episodes/:id/bindings", h.Create)
	api.PATCH("/bindings/:id", h.Update)
	api.DELETE("/bindings/:id", h.Delete)
	api.POST("/bindings/:id/refetch", h.Refetch)
	api.GET("/bindings/:id/danmaku", h.ListDanmaku)
	api.POST("/episodes/:id/file-bindings", h.CreateFromFiles)
	api.POST("/bindings/:id/files", h.AppendFiles)
	api.GET("/bindings/:id/files", h.ListFiles)
	api.POST("/bindings/:id/reparse", h.Reparse)
}
