package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
)

type episodeRequest struct {
	ID int64 `param:"id"`
}

func (r *episodeRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("集 ID 不合法")
	}
	return nil
}

// ExportEpisode GET /api/episodes/:id/export
// 下载一集的弹幕：application/zip，文件名为 <剧名> S01E03.zip（电影为 <片名>.zip），里面平铺各绑定的 <绑定标题>.xml。
// 集不存在、这一集没有可导出的弹幕时 404。
func (h *Handler) ExportEpisode(c *echo.Context) error {
	req, err := request.Bind[episodeRequest](c)
	if err != nil {
		return err
	}
	d := &download{c: c, contentType: "application/zip"}
	return d.finish(h.svc.ExportEpisode(c.Request().Context(), req.ID, d.open))
}
