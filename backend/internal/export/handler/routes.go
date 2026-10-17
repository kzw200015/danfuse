// Package handler 导出的管理 API：只绑定与校验参数、调用 service，把导出的文件直接作为响应体下载。
// 成功时不走统一响应，出错时（写出响应头之前）照常 return error，由全局 errorHandler 输出统一结构。
package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/export/service"
)

// Handler 导出的管理 API，接口在 binding.go、episode.go、season.go，下载的响应头与出错的处理在 download.go。
type Handler struct {
	svc *service.Service
}

func NewHandler(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// Register 把导出挂到管理 API 的组（/api）下。
func (h *Handler) Register(api *echo.Group) {
	api.GET("/bindings/:id/export", h.ExportBinding)
	api.GET("/episodes/:id/export", h.ExportEpisode)
	api.GET("/seasons/:id/export", h.ExportSeason)
}
