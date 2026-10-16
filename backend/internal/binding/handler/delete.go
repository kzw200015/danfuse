package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

// Delete DELETE /api/bindings/:id
// 它的弹幕一起删除。
func (h *Handler) Delete(c *echo.Context) error {
	req, err := request.Bind[bindingRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.OK(c, nil)
}
