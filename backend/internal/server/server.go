// Package server 组装 Echo 实例：中间件、全局错误处理、路由与 HTTP 服务生命周期。
package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/handler"
	"github.com/kzw200015/danfuse/backend/web"
)

type Server struct {
	cfg    config.Server
	logger *slog.Logger
	echo   *echo.Echo
}

func New(cfg config.Server, logger *slog.Logger, h *handler.Handlers) *Server {
	return newServer(cfg, logger, h, web.FS())
}

// newServer 组装 Echo，ui 是要托管的前端文件，测试里换成假的文件系统。
func newServer(cfg config.Server, logger *slog.Logger, h *handler.Handlers, ui fs.FS) *Server {
	e := echo.NewWithConfig(echo.Config{
		Logger:           logger,
		HTTPErrorHandler: errorHandler,
	})

	e.Use(
		middleware.RequestID(),
		middleware.Recover(),
		frontend(ui),
	)

	registerRoutes(e, h)

	return &Server{cfg: cfg, logger: logger, echo: e}
}

// Start 启动 HTTP 服务并阻塞，ctx 取消后优雅退出。
func (s *Server) Start(ctx context.Context) error {
	sc := echo.StartConfig{
		Address:         s.cfg.Addr,
		HideBanner:      true,
		GracefulTimeout: s.cfg.GracefulTimeout,
		BeforeServeFunc: func(hs *http.Server) error {
			hs.ReadTimeout = s.cfg.ReadTimeout
			hs.WriteTimeout = s.cfg.WriteTimeout
			return nil
		},
	}
	if err := sc.Start(ctx, s.echo); err != nil {
		return err
	}
	s.logger.Info("http server stopped")
	return nil
}
