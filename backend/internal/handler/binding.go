package handler

import (
	"math"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
	"github.com/kzw200015/danfuse/backend/internal/service"
)

type BindingHandler struct {
	svc    *service.BindingService
	upload config.DanmakuFile // 一次上传弹幕文件的上限
}

func NewBindingHandler(svc *service.BindingService, upload config.DanmakuFile) *BindingHandler {
	return &BindingHandler{svc: svc, upload: upload}
}

type createBindingRequest struct {
	EpisodeID int64  `param:"id"`
	URL       string `json:"url"`
}

func (r *createBindingRequest) Validate() error {
	if r.EpisodeID < 1 {
		return invalidParam("集 ID 不合法")
	}
	r.URL = strings.TrimSpace(r.URL)
	if r.URL == "" {
		return invalidParam("请粘贴弹幕源的链接")
	}
	return nil
}

// Create POST /api/episodes/:id/bindings {url}
// 当场拉取这个弹幕源的全部弹幕（最长约 25 秒），成功后返回 201 和绑定；拉取失败时不创建。
func (h *BindingHandler) Create(c *echo.Context) error {
	req, err := bind[createBindingRequest](c)
	if err != nil {
		return err
	}
	binding, err := h.svc.Create(c.Request().Context(), req.EpisodeID, req.URL)
	if err != nil {
		return err
	}
	return response.Created(c, binding)
}

// maxOffset 偏移绝对值的上限，秒（一天）。
const maxOffset = 86400

type updateBindingRequest struct {
	ID     int64    `param:"id"`
	Offset *float64 `json:"offset"` // 没传时为 nil，按不合法处理，不当作 0
}

func (r *updateBindingRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("绑定 ID 不合法")
	}
	// JSON 写不出 NaN 和无穷大，超出 float64 范围的数在绑定时就已失败，这里只需检查范围
	if r.Offset == nil || math.Abs(*r.Offset) > maxOffset {
		return invalidParam("偏移必须是 -86400 到 86400 之间的秒数")
	}
	return nil
}

// Update PATCH /api/bindings/:id {offset}
// 改偏移（秒，正数表示弹幕延后），返回绑定。
func (h *BindingHandler) Update(c *echo.Context) error {
	req, err := bind[updateBindingRequest](c)
	if err != nil {
		return err
	}
	binding, err := h.svc.UpdateOffset(c.Request().Context(), req.ID, *req.Offset)
	if err != nil {
		return err
	}
	return response.OK(c, binding)
}

type bindingRequest struct {
	ID int64 `param:"id"`
}

func (r *bindingRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("绑定 ID 不合法")
	}
	return nil
}

// Delete DELETE /api/bindings/:id
// 它的弹幕一起删除。
func (h *BindingHandler) Delete(c *echo.Context) error {
	req, err := bind[bindingRequest](c)
	if err != nil {
		return err
	}
	if err := h.svc.Delete(c.Request().Context(), req.ID); err != nil {
		return err
	}
	return response.OK(c, nil)
}

type refetchBindingRequest struct {
	ID    int64 `param:"id"`
	Clear bool  `json:"clear"` // true 为清空后重新拉取，没传时为重新拉取
}

func (r *refetchBindingRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("绑定 ID 不合法")
	}
	return nil
}

type refetchResponse struct {
	Binding service.BindingView `json:"binding"`
	Added   int64               `json:"added"` // 新增条数；清空后重新拉取时为这次的总条数
}

// Refetch POST /api/bindings/:id/refetch {clear}
// 当场重新拉取这个弹幕源的全部弹幕（最长约 25 秒），返回绑定和新增条数。
// clear 为 false 时只插入新弹幕；为 true 时拉取成功后替换全部弹幕。拉取失败时不改动弹幕。
func (h *BindingHandler) Refetch(c *echo.Context) error {
	req, err := bind[refetchBindingRequest](c)
	if err != nil {
		return err
	}
	binding, added, err := h.svc.Refetch(c.Request().Context(), req.ID, req.Clear)
	if err != nil {
		return err
	}
	return response.OK(c, refetchResponse{Binding: binding, Added: added})
}
