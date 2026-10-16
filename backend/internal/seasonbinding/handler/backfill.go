package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/service"
)

// Backfill POST /api/season-bindings/:id/backfill
// 立即补建，都返回详情：合集在后台补建，返回 202，正在补建时 409，服务正在关闭时 503；文件夹当场补建完，返回 200。
func (h *Handler) Backfill(c *echo.Context) error {
	req, err := request.Bind[seasonBindingRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.Backfill(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	if detail.Kind == service.KindCollection {
		return response.Accepted(c, detail)
	}
	return response.OK(c, detail)
}
