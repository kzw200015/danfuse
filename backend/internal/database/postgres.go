// Package database 负责 PostgreSQL 的连接池与自动迁移（goose 的表锁保证多实例只有一个执行迁移），
// 应用自己的锁（leases 表里的租约，见 TryLease 与登记在 lease.go 的键），以及数据库错误的判断（IsUniqueViolation）。
// 测试用的数据库在子包 dbtest。
package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/config"
)

const connectTimeout = 10 * time.Second

// Connect 创建连接池并检查连通性，不执行迁移（见 Migrate）。用完由调用方关闭。
func Connect(ctx context.Context, cfg config.Database, logger *slog.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse database dsn: %w", err)
	}
	if cfg.MaxConns > 0 {
		poolCfg.MaxConns = cfg.MaxConns
	}
	if cfg.MinConns > 0 {
		poolCfg.MinConns = cfg.MinConns
	}
	if cfg.MaxConnLifetime > 0 {
		poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	}
	if cfg.MaxConnIdleTime > 0 {
		poolCfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	}

	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	connCfg := pool.Config().ConnConfig
	logger.Info("database connected", "host", connCfg.Host, "port", connCfg.Port, "database", connCfg.Database)
	return pool, nil
}
