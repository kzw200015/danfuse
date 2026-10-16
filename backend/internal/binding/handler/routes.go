// Package handler 绑定的管理 API：只绑定与校验参数、调用 service、输出响应。
package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/binding/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/config"
)

// Handler 绑定的管理 API。接口按功能分文件，与 service 里的同名：binding.go（不分种类的改偏移、删除）、link.go、file.go，
// 以及 danmaku.go（查看保存的弹幕）。
type Handler struct {
	svc    *service.Service
	upload config.DanmakuFile // 一次上传弹幕文件的上限
}

func NewHandler(svc *service.Service, upload config.DanmakuFile) *Handler {
	return &Handler{svc: svc, upload: upload}
}

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
