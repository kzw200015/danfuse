// Package database 负责 PostgreSQL 连接池与自动迁移。
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

// NewPool 创建连接池、检查连通性并自动执行数据库迁移，返回的 cleanup 用于关闭连接池。
func NewPool(ctx context.Context, cfg config.Database, logger *slog.Logger) (*pgxpool.Pool, func(), error) {
	pool, err := connect(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	connCfg := pool.Config().ConnConfig
	logger.Info("database connected", "host", connCfg.Host, "port", connCfg.Port, "database", connCfg.Database)

	if err := migrate(ctx, pool, logger); err != nil {
		pool.Close()
		return nil, nil, err
	}
	return pool, pool.Close, nil
}

func connect(ctx context.Context, cfg config.Database) (*pgxpool.Pool, error) {
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
	return pool, nil
}
