package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

// ListSeries GET /api/series
// 全部剧，不分页，每项带海报的图片 ID、季数、集数与绑定统计。
func (h *Handler) ListSeries(c *echo.Context) error {
	series, err := h.svc.ListSeries(c.Request().Context())
	if err != nil {
		return err
	}
	return response.OK(c, series)
}

// GetSeries GET /api/series/:id
// 一部剧的完整子树：季 → 季绑定、集 → 绑定。
func (h *Handler) GetSeries(c *echo.Context) error {
	req, err := request.Bind[seriesRequest](c)
	if err != nil {
		return err
	}
	series, err := h.svc.GetSeries(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	return response.OK(c, series)
}
