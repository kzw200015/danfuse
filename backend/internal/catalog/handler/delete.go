package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

// DeleteSeries DELETE /api/series/:id
// 它的季、集、季绑定、绑定、弹幕和海报一起删除。
func (h *Handler) DeleteSeries(c *echo.Context) error {
	req, err := request.Bind[seriesRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteSeries(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.OK(c, nil)
}

type seasonRequest struct {
	ID int64 `param:"id"`
}

func (r *seasonRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	return nil
}

// DeleteSeason DELETE /api/seasons/:id
// 它的集、季绑定、绑定和弹幕一起删除。
func (h *Handler) DeleteSeason(c *echo.Context) error {
	req, err := request.Bind[seasonRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteSeason(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.OK(c, nil)
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

// DeleteEpisode DELETE /api/episodes/:id
// 它的绑定和弹幕一起删除。
func (h *Handler) DeleteEpisode(c *echo.Context) error {
	req, err := request.Bind[episodeRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.DeleteEpisode(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.OK(c, nil)
}
