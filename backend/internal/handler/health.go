package handler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
)

type HealthHandler struct {
	pool *pgxpool.Pool
}

func NewHealthHandler(pool *pgxpool.Pool) *HealthHandler {
	return &HealthHandler{pool: pool}
}

// Check GET /api/health
func (h *HealthHandler) Check(c *echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
	defer cancel()

	if err := h.pool.Ping(ctx); err != nil {
		return errcode.ErrServiceUnavailable.WithMessage("数据库不可用").Wrap(err)
	}
	return response.OK(c, map[string]string{"status": "ok"})
}
