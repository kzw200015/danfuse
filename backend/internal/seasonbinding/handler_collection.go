package seasonbinding

import (
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

// collectionForm 合集的表单（和预览）：贴的链接、选的候选，加上集号规则与集号对应。嵌进各自的请求结构体，在 Validate 里调用 parse。
type collectionForm struct {
	Link           string `json:"link"`
	CollectionKind string `json:"collectionKind"` // 链接只有一个候选时可以不传；预览不看它
	ruleForm
}

// parse 校验并整理表单：链接为空时 400，集号规则与集号对应见 ruleForm.parse。
func (f *collectionForm) parse() error {
	var err error
	if f.Link, err = parseLink(f.Link); err != nil {
		return err
	}
	f.CollectionKind = strings.TrimSpace(f.CollectionKind)
	return f.ruleForm.parse()
}

// form 交给 service 的表单，parse 之后调用。
func (f *collectionForm) form() CollectionForm {
	return CollectionForm{Link: f.Link, CollectionKind: f.CollectionKind, Mapping: f.mapping, Rule: f.rule}
}

type previewNewCollectionRequest struct {
	SeasonID int64 `param:"id"`
	collectionForm
}

func (r *previewNewCollectionRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
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
	preview, err := h.svc.PreviewNewCollection(c.Request().Context(), req.SeasonID, req.form())
	if err != nil {
		return err
	}
	return response.OK(c, preview)
}

type previewCollectionEditRequest struct {
	ID int64 `param:"id"`
	collectionForm
}

func (r *previewCollectionEditRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
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
	preview, err := h.svc.PreviewCollectionEdit(c.Request().Context(), req.ID, req.form())
	if err != nil {
		return err
	}
	return response.OK(c, preview)
}

type saveCollectionRequest struct {
	ID int64 `param:"id"`
	collectionForm
}

func (r *saveCollectionRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
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
	detail, err := h.svc.SaveCollection(c.Request().Context(), req.ID, req.form())
	if err != nil {
		return err
	}
	return response.OK(c, detail)
}

type createCollectionRequest struct {
	SeasonID int64 `param:"id"`
	collectionForm
}

func (r *createCollectionRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	return r.parse()
}

// CreateCollection POST /api/seasons/:id/season-bindings/collection {link, collectionKind, episodePatterns, mappingFrom, mappingTo}
// 重新识别链接、列出合集（最长约 25 秒），保存季绑定和这次列出的结果，返回 201 和详情；补建随即在后台进行。
func (h *Handler) CreateCollection(c *echo.Context) error {
	req, err := request.Bind[createCollectionRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.CreateCollection(c.Request().Context(), req.SeasonID, req.form())
	if err != nil {
		return err
	}
	return response.Created(c, detail)
}
