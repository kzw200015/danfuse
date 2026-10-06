package handler

import (
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
	"github.com/kzw200015/danfuse/backend/internal/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

type SeasonBindingHandler struct {
	svc *service.SeasonBindingService
}

func NewSeasonBindingHandler(svc *service.SeasonBindingService) *SeasonBindingHandler {
	return &SeasonBindingHandler{svc: svc}
}

const invalidMapping = "集号对应必须是不小于 0 的整数"

// parseRule 解析请求里的集号规则（前后的空白去掉），不合法时为 400，提示由 source.ParseEpisodeRule 给出。
func parseRule(pattern string) (source.EpisodeRule, error) {
	rule, err := source.ParseEpisodeRule(strings.TrimSpace(pattern))
	if err != nil {
		return source.EpisodeRule{}, invalidParam(err.Error())
	}
	return rule, nil
}

// validMapping 集号对应的一端：没传或小于 0 时不合法。
func validMapping(v *int32) bool {
	return v != nil && *v >= 0
}

type previewSeasonBindingRequest struct {
	SeasonID       int64  `param:"id"`
	Link           string `json:"link"`
	EpisodePattern string `json:"episodePattern"` // 集号规则，不传或为空时用内置规则

	rule source.EpisodeRule
}

func (r *previewSeasonBindingRequest) Validate() error {
	if r.SeasonID < 1 {
		return invalidParam("季 ID 不合法")
	}
	r.Link = strings.TrimSpace(r.Link)
	if r.Link == "" {
		return invalidParam("请粘贴合集的链接")
	}
	var err error
	r.rule, err = parseRule(r.EpisodePattern)
	return err
}

// Preview POST /api/seasons/:id/season-bindings/preview {link, episodePattern?}
// 识别链接、列出各个候选合集（当场请求平台，最长约 25 秒），按集号规则认出序号，给出默认的集号对应。不保存任何东西。
func (h *SeasonBindingHandler) Preview(c *echo.Context) error {
	req, err := bind[previewSeasonBindingRequest](c)
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
	SeasonID       int64  `param:"id"`
	Link           string `json:"link"`
	Kind           string `json:"kind"` // 链接只有一个候选时可以不传
	MappingFrom    *int32 `json:"mappingFrom"`
	MappingTo      *int32 `json:"mappingTo"`
	EpisodePattern string `json:"episodePattern"` // 集号规则，不传或为空时用内置规则

	rule source.EpisodeRule
}

func (r *createSeasonBindingRequest) Validate() error {
	if r.SeasonID < 1 {
		return invalidParam("季 ID 不合法")
	}
	r.Link = strings.TrimSpace(r.Link)
	if r.Link == "" {
		return invalidParam("请粘贴合集的链接")
	}
	r.Kind = strings.TrimSpace(r.Kind)
	if !validMapping(r.MappingFrom) || !validMapping(r.MappingTo) {
		return invalidParam(invalidMapping)
	}
	var err error
	r.rule, err = parseRule(r.EpisodePattern)
	return err
}

// Create POST /api/seasons/:id/season-bindings {link, kind, mappingFrom, mappingTo, episodePattern?}
// 重新识别链接、列出合集（最长约 25 秒），保存季绑定和条目，返回 201 和季绑定的详情；补建随即在后台进行。
func (h *SeasonBindingHandler) Create(c *echo.Context) error {
	req, err := bind[createSeasonBindingRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.Create(c.Request().Context(), req.SeasonID, service.CreateSeasonBinding{
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

type seasonBindingRequest struct {
	ID int64 `param:"id"`
}

func (r *seasonBindingRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("季绑定 ID 不合法")
	}
	return nil
}

// Get GET /api/season-bindings/:id
// 季绑定的详情，含条目表与各条目的状态、是否正在补建。不请求平台，条目是上次检查时的合集内容。
func (h *SeasonBindingHandler) Get(c *echo.Context) error {
	req, err := bind[seasonBindingRequest](c)
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
	ID             int64   `param:"id"`
	Follow         *bool   `json:"follow"`
	MappingFrom    *int32  `json:"mappingFrom"`
	MappingTo      *int32  `json:"mappingTo"`
	EpisodePattern *string `json:"episodePattern"` // 集号规则，空串改回内置规则

	rule *source.EpisodeRule
}

func (r *updateSeasonBindingRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("季绑定 ID 不合法")
	}
	if r.Follow == nil && r.MappingFrom == nil && r.MappingTo == nil && r.EpisodePattern == nil {
		return invalidParam("没有要修改的内容")
	}
	if r.MappingFrom != nil && !validMapping(r.MappingFrom) || r.MappingTo != nil && !validMapping(r.MappingTo) {
		return invalidParam(invalidMapping)
	}
	if r.EpisodePattern != nil {
		rule, err := parseRule(*r.EpisodePattern)
		if err != nil {
			return err
		}
		r.rule = &rule
	}
	return nil
}

// Update PATCH /api/season-bindings/:id {follow?, mappingFrom?, mappingTo?, episodePattern?}
// 开关追更、改集号对应和集号规则，只改传了的字段，返回详情（改了集号规则时条目的序号已按新规则重新认出）。
// 打开追更、改了对应或规则时随即在后台补建（正在补建时不另起一轮）。
func (h *SeasonBindingHandler) Update(c *echo.Context) error {
	req, err := bind[updateSeasonBindingRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.Update(c.Request().Context(), req.ID, service.UpdateSeasonBinding{
		Follow: req.Follow, MappingFrom: req.MappingFrom, MappingTo: req.MappingTo, Rule: req.rule,
	})
	if err != nil {
		return err
	}
	return response.OK(c, detail)
}

// Backfill POST /api/season-bindings/:id/backfill
// 立即在后台补建，返回 202；这个季绑定正在补建时返回 409，服务正在关闭时返回 503。
func (h *SeasonBindingHandler) Backfill(c *echo.Context) error {
	req, err := bind[seasonBindingRequest](c)
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
		return invalidParam("季绑定 ID 不合法")
	}
	return nil
}

// Delete DELETE /api/season-bindings/:id?withBindings=true|false
// 进行中的补建随即停下。
func (h *SeasonBindingHandler) Delete(c *echo.Context) error {
	req, err := bind[deleteSeasonBindingRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), req.ID, req.WithBindings); err != nil {
		return err
	}
	return response.OK(c, nil)
}
