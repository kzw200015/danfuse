package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kzw200015/danfuse/backend/internal/app"
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

	application, cleanup, err := app.Init(ctx, configPath)
	if err != nil {
		return err
	}
	defer cleanup()

	return application.Run(ctx)
}
