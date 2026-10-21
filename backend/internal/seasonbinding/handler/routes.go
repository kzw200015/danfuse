// Package handler 季绑定的管理 API：只绑定与校验参数（表单、上传的文件夹）、调用 service、输出响应。
package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/service"
)

// Handler 季绑定的管理 API。接口按功能分文件，与 service 里的同名：season_binding.go（不分种类的详情、开关追更、立即补建、删除，
// 以及两种表单共有的集号规则部分）、collection.go（合集）、folder.go（文件夹）；episode_rule.go 提供默认规则与模板生成。
type Handler struct {
	svc    *service.Service
	upload config.DanmakuFile // 一次上传弹幕文件的上限，上传文件夹用其中按季的那组
}

func NewHandler(svc *service.Service, upload config.DanmakuFile) *Handler {
	return &Handler{svc: svc, upload: upload}
}

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
	api.POST("/episode-rules/template", h.RenderEpisodeTemplate)
}
