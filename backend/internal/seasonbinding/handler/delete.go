package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

// Delete DELETE /api/season-bindings/:id
// 它建出的绑定连同弹幕、文件夹的条目和文件一起删除；进行中的补建随即停下。
func (h *Handler) Delete(c *echo.Context) error {
	req, err := request.Bind[seasonBindingRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.OK(c, nil)
}
