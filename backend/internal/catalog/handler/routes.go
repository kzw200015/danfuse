package handler

import (
	"github.com/labstack/echo/v5"
)

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
