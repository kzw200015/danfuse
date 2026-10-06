package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kzw200015/danfuse/backend/internal/app"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/pkg/logger"
)

func main() {
	configPath := flag.String("config", "", "配置文件路径，不传时只用默认值和环境变量")
	flag.Parse()

	if err := run(*configPath); err != nil {
		slog.Error("server exited with error", "error", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	log := logger.New(cfg.Log)

	// 启动时连接数据库并执行未应用的迁移，不连目录源和 B 站
	pool, err := database.Connect(ctx, cfg.Database, log)
	if err != nil {
		return err
	}
	// Run 返回时同步已写完最终状态、补建已停下，这之后才关闭连接池
	defer pool.Close()
	if err := database.Migrate(ctx, pool, log); err != nil {
		return err
	}

	return app.New(cfg, log, pool).Run(ctx)
}
