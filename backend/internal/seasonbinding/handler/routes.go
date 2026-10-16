package handler

import (
	"github.com/labstack/echo/v5"
)

// Register 把季绑定（合集、文件夹、两种共用的）和默认的集号规则挂到管理 API 的组（/api）下。
func (h *Handler) Register(api *echo.Group) {
	api.POST("/seasons/:id/season-bindings/collection/preview", h.PreviewNewCollection)
	api.POST("/seasons/:id/season-bindings/collection", h.CreateCollection)
	api.POST("/season-bindings/:id/collection/preview", h.PreviewCollectionEdit)
	api.PUT("/season-bindings/:id/collection", h.SaveCollection)
	api.GET("/season-bindings/:id", h.Get)
	api.PATCH("/season-bindings/:id", h.Update)
	api.POST("/season-bindings/:id/backfill", h.Backfill)
	api.DELETE("/season-bindings/:id", h.Delete)
	api.POST("/seasons/:id/season-bindings/folder/preview", h.PreviewNewFolder)
	api.POST("/seasons/:id/season-bindings/folder", h.CreateFolder)
	api.POST("/season-bindings/:id/folder/preview", h.PreviewFolderEdit)
	api.PUT("/season-bindings/:id/folder", h.SaveFolder)
	api.GET("/episode-rules/default", h.DefaultEpisodeRule)
}
