package handler

import (
	"math"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

// maxOffset 偏移绝对值的上限，秒（一天）。
const maxOffset = 86400

type updateBindingRequest struct {
	ID     int64    `param:"id"`
	Offset *float64 `json:"offset"` // 没传时为 nil，按不合法处理，不当作 0
}

func (r *updateBindingRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("绑定 ID 不合法")
	}
	// JSON 写不出 NaN 和无穷大，超出 float64 范围的数在绑定时就已失败，这里只需检查范围
	if r.Offset == nil || math.Abs(*r.Offset) > maxOffset {
		return request.InvalidParam("偏移必须是 -86400 到 86400 之间的秒数")
	}
	return nil
}

// Update PATCH /api/bindings/:id {offset}
// 改偏移（秒，正数表示弹幕延后），返回绑定。
func (h *Handler) Update(c *echo.Context) error {
	req, err := request.Bind[updateBindingRequest](c)
	if err != nil {
		return err
	}
	binding, err := h.svc.UpdateOffset(c.Request().Context(), req.ID, *req.Offset)
	if err != nil {
		return err
	}
	return response.OK(c, binding)
}
