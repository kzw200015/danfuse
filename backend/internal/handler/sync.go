package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
	"github.com/kzw200015/danfuse/backend/internal/service"
)

type SyncHandler struct {
	svc *service.SyncService
}

func NewSyncHandler(svc *service.SyncService) *SyncHandler {
	return &SyncHandler{svc: svc}
}

// Trigger POST /api/sync-runs
// 同步在后台进行，立即返回 202 和这次同步的 ID；已有同步在跑、未配置目录源时返回 409。
func (h *SyncHandler) Trigger(c *echo.Context) error {
	id, err := h.svc.Trigger(c.Request().Context())
	if err != nil {
		return err
	}
	return response.Accepted(c, map[string]int64{"id": id})
}

// List GET /api/sync-runs
// 最近 20 次同步，新的在前，不含警告正文。
func (h *SyncHandler) List(c *echo.Context) error {
	runs, err := h.svc.ListRuns(c.Request().Context())
	if err != nil {
		return err
	}
	return response.OK(c, runs)
}

type syncRunRequest struct {
	ID int64 `param:"id"`
}

func (r *syncRunRequest) Validate() error {
	if r.ID < 1 {
		return invalidParam("同步记录 ID 不合法")
	}
	return nil
}

// Get GET /api/sync-runs/:id
// 一次同步的详情，含警告。
func (h *SyncHandler) Get(c *echo.Context) error {
	req, err := bind[syncRunRequest](c)
	if err != nil {
		return err
	}
	run, err := h.svc.GetRun(c.Request().Context(), req.ID)
	if err != nil {
		return err
	}
	return response.OK(c, run)
}
