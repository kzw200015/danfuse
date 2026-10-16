package server

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

type HealthHandler struct {
	svc *HealthService
}

func NewHealthHandler(svc *HealthService) *HealthHandler {
	return &HealthHandler{svc: svc}
}

// Check GET /api/health
func (h *HealthHandler) Check(c *echo.Context) error {
	if err := h.svc.Check(c.Request().Context()); err != nil {
		return err
	}
	return response.OK(c, map[string]string{"status": "ok"})
}
