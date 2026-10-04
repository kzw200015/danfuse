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

// migrate 执行所有未应用的 goose 迁移（迁移文件已嵌入二进制）。
// 使用 PostgreSQL advisory lock，多实例同时启动时只有一个实例会执行迁移。
func migrate(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) error {
	migrations, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("create migration locker: %w", err)
	}

	// 基于连接池包装出 *sql.DB 供 goose 使用，关闭它不会关闭底层的 pgxpool
	sqlDB := stdlib.OpenDBFromPool(pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations, goose.WithSessionLocker(locker))
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
