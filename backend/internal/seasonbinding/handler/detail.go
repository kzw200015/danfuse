package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

// Get GET /api/season-bindings/:id
// 季绑定的详情：季绑定，加上各条目派生的结果（items）和没有条目、绑定还留着的槽位（emptySlots）。不请求平台。
func (h *Handler) Get(c *echo.Context) error {
	req, err := request.Bind[seasonBindingRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.Get(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	return response.OK(c, detail)
}
