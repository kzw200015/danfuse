package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

type updateSeasonBindingRequest struct {
	ID     int64 `param:"id"`
	Follow *bool `json:"follow"`
}

func (r *updateSeasonBindingRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
	if r.Follow == nil {
		return request.InvalidParam("没有要修改的内容")
	}
	return nil
}

// Update PATCH /api/season-bindings/:id {follow}
// 开关追更（只有合集的季绑定有，文件夹的 400），返回详情；打开时随即在后台补建（正在补建时不另起一轮）。
func (h *Handler) Update(c *echo.Context) error {
	req, err := request.Bind[updateSeasonBindingRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.SetFollow(c.Request().Context(), req.ID, *req.Follow)
	if err != nil {
		return err
	}
	return response.OK(c, detail)
}
