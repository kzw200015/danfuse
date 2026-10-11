package seasonbinding

import (
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/httpx/response"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// Handler 季绑定的管理 API（含按季上传，见 handler_season_upload.go），路由注册在 server/router.go。
type Handler struct {
	svc    *Service
	upload config.DanmakuFile // 一次上传弹幕文件的上限，按季上传用其中按季的那组
}

func NewHandler(svc *Service, upload config.DanmakuFile) *Handler {
	return &Handler{svc: svc, upload: upload}
}

const invalidMapping = "集号对应必须是不小于 0 的整数"

// parseRule 解析请求里的集号规则，不合法时为 400，提示由 source.ParseEpisodeRule 给出。
func parseRule(patterns []string) (source.EpisodeRule, error) {
	rule, err := source.ParseEpisodeRule(patterns)
	if err != nil {
		return source.EpisodeRule{}, request.InvalidParam(err.Error())
	}
	return rule, nil
}

// parseLink 去掉贴的链接前后的空白，为空时为 400。
func parseLink(link string) (string, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return "", request.InvalidParam("请粘贴合集的链接")
	}
	return link, nil
}

type defaultEpisodeRuleResponse struct {
	EpisodePatterns []string `json:"episodePatterns"`
}

// DefaultEpisodeRule GET /api/episode-rules/default
// 默认的集号规则：管理界面预览时作为编辑的起点，"恢复默认"时取它。
func (h *Handler) DefaultEpisodeRule(c *echo.Context) error {
	return response.OK(c, defaultEpisodeRuleResponse{EpisodePatterns: source.DefaultEpisodeRule().Patterns()})
}

// validMapping 集号对应的一端：没传或小于 0 时不合法。
func validMapping(v *int32) bool {
	return v != nil && *v >= 0
}

type previewSeasonBindingRequest struct {
	SeasonID        int64    `param:"id"`
	Link            string   `json:"link"`
	EpisodePatterns []string `json:"episodePatterns"` // 集号规则，必须传；管理界面先取默认规则

	rule source.EpisodeRule
}

func (r *previewSeasonBindingRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	var err error
	if r.Link, err = parseLink(r.Link); err != nil {
		return err
	}
	r.rule, err = parseRule(r.EpisodePatterns)
	return err
}

// Preview POST /api/seasons/:id/season-bindings/preview {link, episodePatterns}
// 识别链接、列出各个候选合集（当场请求平台，最长约 25 秒），按集号规则认出序号。不保存任何东西。
func (h *Handler) Preview(c *echo.Context) error {
	req, err := request.Bind[previewSeasonBindingRequest](c)
	if err != nil {
		return err
	}
	preview, err := h.svc.Preview(c.Request().Context(), req.SeasonID, req.Link, req.rule)
	if err != nil {
		return err
	}
	return response.OK(c, preview)
}

type createSeasonBindingRequest struct {
	SeasonID        int64    `param:"id"`
	Link            string   `json:"link"`
	Kind            string   `json:"kind"` // 链接只有一个候选时可以不传
	MappingFrom     *int32   `json:"mappingFrom"`
	MappingTo       *int32   `json:"mappingTo"`
	EpisodePatterns []string `json:"episodePatterns"` // 集号规则，必须传；管理界面先取默认规则

	rule source.EpisodeRule
}

func (r *createSeasonBindingRequest) Validate() error {
	if r.SeasonID < 1 {
		return request.InvalidParam("季 ID 不合法")
	}
	var err error
	if r.Link, err = parseLink(r.Link); err != nil {
		return err
	}
	r.Kind = strings.TrimSpace(r.Kind)
	if !validMapping(r.MappingFrom) || !validMapping(r.MappingTo) {
		return request.InvalidParam(invalidMapping)
	}
	r.rule, err = parseRule(r.EpisodePatterns)
	return err
}

// Create POST /api/seasons/:id/season-bindings {link, kind, mappingFrom, mappingTo, episodePatterns}
// 重新识别链接、列出合集（最长约 25 秒），保存季绑定和条目，返回 201 和季绑定的详情；补建随即在后台进行。
func (h *Handler) Create(c *echo.Context) error {
	req, err := request.Bind[createSeasonBindingRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.Create(c.Request().Context(), req.SeasonID, CreateParams{
		Link:    req.Link,
		Kind:    req.Kind,
		Mapping: source.Mapping{From: int(*req.MappingFrom), To: int(*req.MappingTo)},
		Rule:    req.rule,
	})
	if err != nil {
		return err
	}
	return response.Created(c, detail)
}

type previewChangeRequest struct {
	ID              int64    `param:"id"`
	Link            string   `json:"link"`
	EpisodePatterns []string `json:"episodePatterns"` // 集号规则，必须传

	rule source.EpisodeRule
}

func (r *previewChangeRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
	var err error
	if r.Link, err = parseLink(r.Link); err != nil {
		return err
	}
	r.rule, err = parseRule(r.EpisodePatterns)
	return err
}

// PreviewChange POST /api/season-bindings/:id/preview {link, episodePatterns}
// 编辑合集的季绑定时的预览：同季上的预览，另外标出现在绑着的候选（current）、换成各个候选时会删掉的绑定数（removedBindings），
// 以及处理过的条目的绑定建在哪一集（handledEpisode）。不保存任何东西。
func (h *Handler) PreviewChange(c *echo.Context) error {
	req, err := request.Bind[previewChangeRequest](c)
	if err != nil {
		return err
	}
	preview, err := h.svc.PreviewChange(c.Request().Context(), req.ID, req.Link, req.rule)
	if err != nil {
		return err
	}
	return response.OK(c, preview)
}

type seasonBindingRequest struct {
	ID int64 `param:"id"`
}

func (r *seasonBindingRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
	return nil
}

// Get GET /api/season-bindings/:id
// 季绑定的详情，含条目表与各条目的状态、是否正在补建。不请求平台，条目是上次检查时的合集内容。
func (h *Handler) Get(c *echo.Context) error {
	req, err := request.Bind[seasonBindingRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.Get(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	return response.OK(c, detail)
}

type updateSeasonBindingRequest struct {
	ID              int64    `param:"id"`
	Follow          *bool    `json:"follow"`
	MappingFrom     *int32   `json:"mappingFrom"`
	MappingTo       *int32   `json:"mappingTo"`
	EpisodePatterns []string `json:"episodePatterns"` // 集号规则，不传或为 null 时不改
	Link            *string  `json:"link"`            // 换合集：贴的链接，不传或为 null 时不换
	Kind            string   `json:"kind"`            // 换合集时选的候选，链接只有一个候选时可以不传

	rule       *source.EpisodeRule
	collection *CollectionChoice
}

func (r *updateSeasonBindingRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
	if r.Follow == nil && r.MappingFrom == nil && r.MappingTo == nil && r.EpisodePatterns == nil && r.Link == nil {
		return request.InvalidParam("没有要修改的内容")
	}
	if r.MappingFrom != nil && !validMapping(r.MappingFrom) || r.MappingTo != nil && !validMapping(r.MappingTo) {
		return request.InvalidParam(invalidMapping)
	}
	if r.EpisodePatterns != nil {
		rule, err := parseRule(r.EpisodePatterns)
		if err != nil {
			return err
		}
		r.rule = &rule
	}
	if r.Link != nil {
		link, err := parseLink(*r.Link)
		if err != nil {
			return err
		}
		r.collection = &CollectionChoice{Link: link, Kind: strings.TrimSpace(r.Kind)}
	}
	return nil
}

// Update PATCH /api/season-bindings/:id {follow?, mappingFrom?, mappingTo?, episodePatterns?, link?, kind?}
// 开关追更、改集号对应和集号规则、换合集，只改传了的字段，返回详情（改了集号规则、换了合集时条目已按新规则重新认出）。
// 传了 link 时重新识别链接、列出合集（最长约 25 秒），正在补建时返回 409；换成另一个合集时，
// 它建出的、弹幕源不在新合集里的绑定连同弹幕删除。打开追更、改了对应或规则、换了合集时随即在后台补建（正在补建时不另起一轮）。
func (h *Handler) Update(c *echo.Context) error {
	req, err := request.Bind[updateSeasonBindingRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.Update(c.Request().Context(), req.ID, UpdateParams{
		Follow: req.Follow, MappingFrom: req.MappingFrom, MappingTo: req.MappingTo, Rule: req.rule, Collection: req.collection,
	})
	if err != nil {
		return err
	}
	return response.OK(c, detail)
}

// Backfill POST /api/season-bindings/:id/backfill
// 立即在后台补建，返回 202；这个季绑定正在补建时返回 409，服务正在关闭时返回 503。
func (h *Handler) Backfill(c *echo.Context) error {
	req, err := request.Bind[seasonBindingRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.Backfill(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.Accepted(c, nil)
}

type deleteSeasonBindingRequest struct {
	ID           int64 `param:"id"`
	WithBindings bool  `query:"withBindings"` // 同时删除它建出的绑定，默认 false：保留下来，变成普通绑定
}

func (r *deleteSeasonBindingRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
	return nil
}

// Delete DELETE /api/season-bindings/:id?withBindings=true|false
// 进行中的补建随即停下。
func (h *Handler) Delete(c *echo.Context) error {
	req, err := request.Bind[deleteSeasonBindingRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), req.ID, req.WithBindings); err != nil {
		return err
	}
	return response.OK(c, nil)
}
