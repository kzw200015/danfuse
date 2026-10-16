// Package handler 目录的管理 API：剧、季、集的浏览、删除与海报（series.go），同步（sync.go），文件与 service 里的同名。
// 只绑定与校验参数、调用 service、输出响应。
package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/catalog/service"
)

// Handler 目录的管理 API：剧、季、集的浏览、删除与海报。
type Handler struct {
	svc *service.Service
}

func NewHandler(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// SyncHandler 同步的管理 API：触发同步、同步记录。
type SyncHandler struct {
	svc *service.SyncService
}

func NewSyncHandler(svc *service.SyncService) *SyncHandler {
	return &SyncHandler{svc: svc}
}

// Register 把目录的浏览、删除、海报挂到管理 API 的组（/api）下。
func (h *Handler) Register(api *echo.Group) {
	api.GET("/series", h.ListSeries)
	api.GET("/series/:id", h.GetSeries)
	api.DELETE("/series/:id", h.DeleteSeries)
	api.DELETE("/seasons/:id", h.DeleteSeason)
	api.DELETE("/episodes/:id", h.DeleteEpisode)
	api.GET("/images/:id", h.GetImage)
}

// Register 把同步的触发与同步记录挂到管理 API 的组（/api）下。
func (h *SyncHandler) Register(api *echo.Group) {
	api.POST("/sync-runs", h.Trigger)
	api.GET("/sync-runs", h.List)
	api.GET("/sync-runs/latest", h.Latest)
	api.GET("/sync-runs/:id", h.Get)
}
