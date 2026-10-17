package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
)

type seasonRequest struct {
	ID int64 `param:"id"`
}

func (r *seasonRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	return nil
}

// ExportSeason GET /api/seasons/:id/export
// 下载一季的弹幕：application/zip，文件名为 <剧名> S01.zip，里面是 <剧名> S01/S01E03/<绑定标题>.xml。
// 季不存在、这一季没有可导出的弹幕时 404。zip 边读边写，写出响应头之后出错时中止连接（见 download.finish）。
func (h *Handler) ExportSeason(c *echo.Context) error {
	req, err := request.Bind[seasonRequest](c)
	if err != nil {
		return err
	}
	d := &download{c: c, contentType: "application/zip"}
	return d.finish(h.svc.ExportSeason(c.Request().Context(), req.ID, d.open))
}
