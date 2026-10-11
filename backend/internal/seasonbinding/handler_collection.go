package seasonbinding

import (
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/httpx/response"
)

type createCollectionRequest struct {
	SeasonID       int64  `param:"id"`
	Link           string `json:"link"`
	CollectionKind string `json:"collectionKind"` // 链接只有一个候选时可以不传
	ruleForm
}

func (r *createCollectionRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	var err error
	if r.Link, err = parseLink(r.Link); err != nil {
		return err
	}
	r.CollectionKind = strings.TrimSpace(r.CollectionKind)
	return r.parse()
}

// CreateCollection POST /api/seasons/:id/season-bindings/collection {link, collectionKind, episodePatterns, mappingFrom, mappingTo}
// 重新识别链接、列出合集（最长约 25 秒），保存季绑定和这次列出的结果，返回 201 和详情；补建随即在后台进行。
func (h *Handler) CreateCollection(c *echo.Context) error {
	req, err := request.Bind[createCollectionRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.CreateCollection(c.Request().Context(), req.SeasonID, CollectionForm{
		Link: req.Link, CollectionKind: req.CollectionKind, Mapping: req.mapping, Rule: req.rule,
	})
	if err != nil {
		return err
	}
	return response.Created(c, detail)
}
