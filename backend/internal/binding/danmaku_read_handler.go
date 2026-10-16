package binding

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

type listDanmakuRequest struct {
	ID     int64  `param:"id"`
	FromMs *int32 `query:"fromMs"` // 跳转：只取弹幕源时间在它及以后的，毫秒；没传时不限
	After  string `query:"after"`  // 上一页给出的游标，没传时从头取
}

func (r *listDanmakuRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("绑定 ID 不合法")
	}
	return nil
}

// ListDanmaku GET /api/bindings/:id/danmaku?fromMs=&after=
// 绑定保存的弹幕，时间未校正，按时间升序分页；返回 {items, next}，next 为下一页的游标，没有下一页时为 null。
// fromMs 用于跳转，翻页时与 after 一起照传。
func (h *Handler) ListDanmaku(c *echo.Context) error {
	req, err := request.Bind[listDanmakuRequest](c)
	if err != nil {
		return err
	}
	page, err := h.svc.ListDanmaku(c.Request().Context(), req.ID, req.FromMs, req.After)
	if err != nil {
		return err
	}
	return response.OK(c, page)
}
