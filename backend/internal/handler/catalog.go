package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
	"github.com/kzw200015/danfuse/backend/internal/service"
)

type CatalogHandler struct {
	svc *service.CatalogService
}

func NewCatalogHandler(svc *service.CatalogService) *CatalogHandler {
	return &CatalogHandler{svc: svc}
}

// ListSeries GET /api/series
// 全部剧，不分页，每项带季数、集数。
func (h *CatalogHandler) ListSeries(c *echo.Context) error {
	series, err := h.svc.ListSeries(c.Request().Context())
	if err != nil {
		return err
	}
	return response.OK(c, series)
}

type seriesRequest struct {
	ID int64 `param:"id"`
}

func (r *seriesRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("剧 ID 不合法")
	}
	return nil
}

// GetSeries GET /api/series/:id
// 一部剧的完整子树：季 → 集。
func (h *CatalogHandler) GetSeries(c *echo.Context) error {
	req, err := bind[seriesRequest](c)
	if err != nil {
		return err
	}
	series, err := h.svc.GetSeries(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	return response.OK(c, series)
}
