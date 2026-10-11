package seasonbinding

import (
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/httpx/response"
)

type previewNewCollectionRequest struct {
	SeasonID int64  `param:"id"`
	Link     string `json:"link"`
	ruleForm
}

func (r *previewNewCollectionRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	var err error
	if r.Link, err = parseLink(r.Link); err != nil {
		return err
	}
	return r.parse()
}

// PreviewNewCollection POST /api/seasons/:id/season-bindings/collection/preview {link, episodePatterns, mappingFrom, mappingTo}
// 新建的预览：识别链接、列出每个候选合集（最长约 25 秒），各自按集号规则和集号对应派生，不保存。
func (h *Handler) PreviewNewCollection(c *echo.Context) error {
	req, err := request.Bind[previewNewCollectionRequest](c)
	if err != nil {
		return err
	}
	preview, err := h.svc.PreviewNewCollection(c.Request().Context(), req.SeasonID, CollectionForm{
		Link: req.Link, Mapping: req.mapping, Rule: req.rule,
	})
	if err != nil {
		return err
	}
	return response.OK(c, preview)
}

type previewCollectionEditRequest struct {
	ID   int64  `param:"id"`
	Link string `json:"link"`
	ruleForm
}

func (r *previewCollectionEditRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
	var err error
	if r.Link, err = parseLink(r.Link); err != nil {
		return err
	}
	return r.parse()
}

// PreviewCollectionEdit POST /api/season-bindings/:id/collection/preview {link, episodePatterns, mappingFrom, mappingTo}
// 编辑的预览：同新建的预览，另外与季绑定现在的槽位比较（保存后替换、删除哪些），current 标出保存的候选。不保存。
func (h *Handler) PreviewCollectionEdit(c *echo.Context) error {
	req, err := request.Bind[previewCollectionEditRequest](c)
	if err != nil {
		return err
	}
	preview, err := h.svc.PreviewCollectionEdit(c.Request().Context(), req.ID, CollectionForm{
		Link: req.Link, Mapping: req.mapping, Rule: req.rule,
	})
	if err != nil {
		return err
	}
	return response.OK(c, preview)
}

type saveCollectionRequest struct {
	ID             int64  `param:"id"`
	Link           string `json:"link"`
	CollectionKind string `json:"collectionKind"` // 链接只有一个候选时可以不传
	ruleForm
}

func (r *saveCollectionRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
	var err error
	if r.Link, err = parseLink(r.Link); err != nil {
		return err
	}
	r.CollectionKind = strings.TrimSpace(r.CollectionKind)
	return r.parse()
}

// SaveCollection PUT /api/season-bindings/:id/collection {link, collectionKind, episodePatterns, mappingFrom, mappingTo}
// 保存表单：重新列出合集（最长约 25 秒），删掉没有条目的槽位，返回详情；替换和新建随即在后台补建。
// 正在补建或保存时 409。
func (h *Handler) SaveCollection(c *echo.Context) error {
	req, err := request.Bind[saveCollectionRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.SaveCollection(c.Request().Context(), req.ID, CollectionForm{
		Link: req.Link, CollectionKind: req.CollectionKind, Mapping: req.mapping, Rule: req.rule,
	})
	if err != nil {
		return err
	}
	return response.OK(c, detail)
}

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
