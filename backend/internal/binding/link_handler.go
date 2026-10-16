package binding

import (
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

type createBindingRequest struct {
	EpisodeID int64  `param:"id"`
	URL       string `json:"url"`
}

func (r *createBindingRequest) Validate() error {
	if r.EpisodeID < 1 {
		return request.InvalidParam("集 ID 不合法")
	}
	r.URL = strings.TrimSpace(r.URL)
	if r.URL == "" {
		return request.InvalidParam("请粘贴弹幕源的链接")
	}
	return nil
}

// Create POST /api/episodes/:id/bindings {url}
// 当场拉取这个弹幕源的全部弹幕（最长约 25 秒），成功后返回 201 和绑定；拉取失败时不创建。
func (h *Handler) Create(c *echo.Context) error {
	req, err := request.Bind[createBindingRequest](c)
	if err != nil {
		return err
	}
	binding, err := h.svc.Create(c.Request().Context(), req.EpisodeID, req.URL)
	if err != nil {
		return err
	}
	return response.Created(c, binding)
}

type refetchBindingRequest struct {
	ID    int64 `param:"id"`
	Clear bool  `json:"clear"` // true 为清空后重新拉取，没传时为重新拉取
}

func (r *refetchBindingRequest) Validate() error {
	if r.ID < 1 {
		return request.InvalidParam("绑定 ID 不合法")
	}
	return nil
}

type refetchResponse struct {
	Binding View  `json:"binding"`
	Added   int64 `json:"added"` // 新增条数；清空后重新拉取时为这次的总条数
}

// Refetch POST /api/bindings/:id/refetch {clear}
// 当场重新拉取这个弹幕源的全部弹幕（最长约 25 秒），返回绑定和新增条数。
// clear 为 false 时只插入新弹幕；为 true 时拉取成功后替换全部弹幕。拉取失败时不改动弹幕。
func (h *Handler) Refetch(c *echo.Context) error {
	req, err := request.Bind[refetchBindingRequest](c)
	if err != nil {
		return err
	}
	binding, added, err := h.svc.Refetch(c.Request().Context(), req.ID, req.Clear)
	if err != nil {
		return err
	}
	return response.OK(c, refetchResponse{Binding: binding, Added: added})
}
