package seasonbinding

import (
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// Handler 季绑定的管理 API，路由注册在 server/router.go。两种季绑定共用的（详情、开关追更、立即补建、删除）在这里，
// 合集的在 handler_collection.go，文件夹的在 handler_folder.go。
type Handler struct {
	svc    *Service
	upload config.DanmakuFile // 一次上传弹幕文件的上限，上传文件夹用其中按季的那组
}

func NewHandler(svc *Service, upload config.DanmakuFile) *Handler {
	return &Handler{svc: svc, upload: upload}
}

const invalidMapping = "集号对应必须是不小于 0 的整数"

// ruleForm 两种季绑定的表单（和预览）共有的部分：集号规则与集号对应。嵌进各自的请求结构体，在 Validate 里调用 parse。
type ruleForm struct {
	EpisodePatterns []string `json:"episodePatterns"` // 集号规则，必须传；管理界面先取默认规则
	MappingFrom     *int32   `json:"mappingFrom"`
	MappingTo       *int32   `json:"mappingTo"`

	rule    source.EpisodeRule
	mapping source.Mapping
}

// parse 校验并解析集号规则与集号对应：对应没传或小于 0 时 400，规则不合法时 400（提示由 source.ParseEpisodeRule 给出）。
func (f *ruleForm) parse() error {
	if !validMapping(f.MappingFrom) || !validMapping(f.MappingTo) {
		return request.InvalidParam(invalidMapping)
	}
	f.mapping = source.Mapping{From: int(*f.MappingFrom), To: int(*f.MappingTo)}
	rule, err := source.ParseEpisodeRule(f.EpisodePatterns)
	if err != nil {
		return request.InvalidParam(err.Error())
	}
	f.rule = rule
	return nil
}

// validMapping 集号对应的一端：没传或小于 0 时不合法。
func validMapping(v *int32) bool {
	return v != nil && *v >= 0
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
// 季绑定的详情：季绑定，加上各条目派生的结果（items）和没有条目、绑定还留着的槽位（emptySlots）。不请求平台。
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
	ID     int64 `param:"id"`
	Follow *bool `json:"follow"`
}

func (r *updateSeasonBindingRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("季绑定 ID 不合法")
	}
	if r.Follow == nil {
		return request.InvalidParam("没有要修改的内容")
	}
	return nil
}

// Update PATCH /api/season-bindings/:id {follow}
// 开关追更（只有合集的季绑定有，文件夹的 400），返回详情；打开时随即在后台补建（正在补建时不另起一轮）。
func (h *Handler) Update(c *echo.Context) error {
	req, err := request.Bind[updateSeasonBindingRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.SetFollow(c.Request().Context(), req.ID, *req.Follow)
	if err != nil {
		return err
	}
	return response.OK(c, detail)
}

// Backfill POST /api/season-bindings/:id/backfill
// 立即补建，都返回详情：合集在后台补建，返回 202，正在补建时 409，服务正在关闭时 503；文件夹当场补建完，返回 200。
func (h *Handler) Backfill(c *echo.Context) error {
	req, err := request.Bind[seasonBindingRequest](c)
	if err != nil {
		return err
	}
	detail, err := h.svc.Backfill(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	if detail.Kind == kindCollection {
		return response.Accepted(c, detail)
	}
	return response.OK(c, detail)
}

// Delete DELETE /api/season-bindings/:id
// 它建出的绑定连同弹幕、文件夹的条目和文件一起删除；进行中的补建随即停下。
func (h *Handler) Delete(c *echo.Context) error {
	req, err := request.Bind[seasonBindingRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.OK(c, nil)
}
