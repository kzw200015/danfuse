package database

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/kzw200015/danfuse/backend/db"
)

// Migrate 执行所有未应用的 goose 迁移（迁移文件嵌入二进制）。
// 使用 goose 自带的表锁（goose_lock 表里的租约，租约时长与续约间隔同应用的租约），多实例同时启动时只有一个实例会执行迁移，
// 其他实例等它做完（最多等 goose 默认的 5 分钟）。
func Migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	migrations, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}

	locker, err := lock.NewPostgresTableLocker(
		lock.WithTableLeaseDuration(leaseTTL),
		lock.WithTableHeartbeatInterval(leaseRenewInterval),
		lock.WithTableLogger(logger),
	)
	if err != nil {
		return fmt.Errorf("create migration locker: %w", err)
	}

	// 基于连接池包装出 *sql.DB 供 goose 使用，关闭它不会关闭底层的 pgxpool
	sqlDB := stdlib.OpenDBFromPool(pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations, goose.WithLocker(locker))
	if err != nil {
		_ = sqlDB.Close() // 已有更重要的错误要返回，忽略关闭错误
		return fmt.Errorf("create migration provider: %w", err)
	}
	// Provider.Close 会关闭传入的 sqlDB
	defer func() {
		if err := provider.Close(); err != nil {
			logger.Warn("close migration provider failed", "error", err)
		}
	}()

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	for _, r := range results {
		logger.Info("migration applied", "version", r.Source.Version, "file", r.Source.Path, "duration", r.Duration)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("get database version: %w", err)
	}
	logger.Info("database migrated", "version", version, "applied", len(results))
	return nil
}
