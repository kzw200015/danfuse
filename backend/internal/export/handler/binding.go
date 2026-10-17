package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
)

type bindingRequest struct {
	ID int64 `param:"id"`
}

func (r *bindingRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("绑定 ID 不合法")
	}
	return nil
}

// ExportBinding GET /api/bindings/:id/export
// 导出一个绑定的弹幕：application/xml，文件名为 <绑定标题>.xml。绑定不存在时 404。
func (h *Handler) ExportBinding(c *echo.Context) error {
	req, err := request.Bind[bindingRequest](c)
	if err != nil {
		return err
	}
	d := &download{c: c, contentType: "application/xml"}
	return d.finish(h.svc.ExportBinding(c.Request().Context(), req.ID, d.open))
}
