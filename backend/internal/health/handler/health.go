package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

// Check GET /api/health
func (h *Handler) Check(c *echo.Context) error {
	if err := h.svc.Check(c.Request().Context()); err != nil {
		return err
	}
	return response.OK(c, map[string]string{"status": "ok"})
}
