package handler

import (
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
	"github.com/kzw200015/danfuse/backend/internal/service"
)

type BindingHandler struct {
	svc *service.BindingService
}

func NewBindingHandler(svc *service.BindingService) *BindingHandler {
	return &BindingHandler{svc: svc}
}

type createBindingRequest struct {
	EpisodeID int64  `param:"id"`
	URL       string `json:"url"`
}

func (r *createBindingRequest) Validate() error {
	if r.EpisodeID < 1 {
		return invalidParam("集 ID 不合法")
	}
	r.URL = strings.TrimSpace(r.URL)
	if r.URL == "" {
		return invalidParam("请粘贴弹幕源的链接")
	}
	return nil
}

// Create POST /api/episodes/:id/bindings {url}
// 当场拉取这个弹幕源的全部弹幕（最长约 25 秒），成功后返回 201 和绑定；拉取失败时不创建。
func (h *BindingHandler) Create(c *echo.Context) error {
	req, err := bind[createBindingRequest](c)
	if err != nil {
		return err
	}
	binding, err := h.svc.Create(c.Request().Context(), req.EpisodeID, req.URL)
	if err != nil {
		return err
	}
	return response.Created(c, binding)
}
