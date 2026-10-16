package server

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
)

// pingTimeout 健康检查等数据库应答的上限。
const pingTimeout = 2 * time.Second

// HealthService 健康检查：服务能连上数据库即为健康。
type HealthService struct {
	pool *pgxpool.Pool
}

func NewHealthService(pool *pgxpool.Pool) *HealthService {
	return &HealthService{pool: pool}
}

// Check ping 一次数据库，不通时返回 503。
func (s *HealthService) Check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	if err := s.pool.Ping(ctx); err != nil {
		return apierr.ErrServiceUnavailable.WithMessage("数据库不可用").Wrap(err)
	}
	return nil
}
