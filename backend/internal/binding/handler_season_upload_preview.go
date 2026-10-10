package binding

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/httpx/response"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

type previewSeasonUploadRequest struct {
	SeasonID        int64    `param:"id"`
	Labels          []string `json:"labels"`          // 条目名称，"所选文件夹名 / 子目录名或文件名去掉扩展名"
	EpisodePatterns []string `json:"episodePatterns"` // 集号规则，必须传；管理界面先取默认规则

	rule source.EpisodeRule
}

func (r *previewSeasonUploadRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	if len(r.Labels) == 0 {
		return request.InvalidParam("请选择弹幕文件夹")
	}
	rule, err := source.ParseEpisodeRule(r.EpisodePatterns)
	if err != nil {
		return request.InvalidParam(err.Error())
	}
	r.rule = rule
	return nil
}

// PreviewSeasonUpload POST /api/seasons/:id/file-bindings/preview {labels, episodePatterns}
// 按季上传的预览：按集号规则从条目名称认出序号，返回 {items: [{label, number, reason}]}，顺序与 labels 相同。
// 不上传文件、不保存任何东西。
func (h *Handler) PreviewSeasonUpload(c *echo.Context) error {
	req, err := request.Bind[previewSeasonUploadRequest](c)
	if err != nil {
		return err
	}
	preview, err := h.svc.PreviewSeasonUpload(c.Request().Context(), req.SeasonID, req.Labels, req.rule)
	if err != nil {
		return err
	}
	return response.OK(c, preview)
}
