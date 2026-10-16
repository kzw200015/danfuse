// Package handler 目录的管理 API：浏览（browse.go）、海报（image.go）、删除（delete.go）与同步（sync.go），文件与 service 里的同名。
// 只绑定与校验参数、调用 service、输出响应。
package handler

import (
	"github.com/kzw200015/danfuse/backend/internal/catalog/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
)

// Handler 目录的管理 API：浏览、海报、删除，路由见同包的 routes.go。
type Handler struct {
	svc *service.Service
}

func NewHandler(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

type seriesRequest struct {
	ID int64 `param:"id"`
}

func (r *seriesRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("剧 ID 不合法")
	}
	return nil
}
