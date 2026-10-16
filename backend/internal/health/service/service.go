// Package service 健康检查（GET /api/health）：服务能连上数据库即为健康。
package service

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
)

// pingTimeout 健康检查等数据库应答的上限。
const pingTimeout = 2 * time.Second

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Check ping 一次数据库，不通时返回 503。
func (s *Service) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := s.pool.Ping(ctx); err != nil {
		return apierr.ErrServiceUnavailable.WithMessage("数据库不可用").Wrap(err)
	}
	return nil
}
