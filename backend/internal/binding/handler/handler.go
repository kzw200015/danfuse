// Package handler 绑定的管理 API：只绑定与校验参数、调用 service、输出响应。
package handler

import (
	"github.com/kzw200015/danfuse/backend/internal/binding/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
)

// Handler 绑定的管理 API，路由见同包的 routes.go。
type Handler struct {
	svc    *service.Service
	upload config.DanmakuFile // 一次上传弹幕文件的上限
}

func NewHandler(svc *service.Service, upload config.DanmakuFile) *Handler {
	return &Handler{svc: svc, upload: upload}
}

type episodeRequest struct {
	ID int64 `param:"id"`
}

func (r *episodeRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("集 ID 不合法")
	}
	return nil
}

type bindingRequest struct {
	ID int64 `param:"id"`
}

func (r *bindingRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("绑定 ID 不合法")
	}
	return nil
}
