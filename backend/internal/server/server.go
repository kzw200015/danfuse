// Package server 组装 Echo 实例：中间件、全局错误处理、路由与 HTTP 服务生命周期。
package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/web"
)

// 整个请求的读、写默认不限时（server.read_timeout / write_timeout 为 0），上传整季的文件、当场拉取弹幕都可能很久；
// 只固定限制读请求头和空闲的长连接，防止慢速连接占着不放。
const (
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
)

type Server struct {
	cfg    config.Server
	logger *slog.Logger
	echo   *echo.Echo
}

// New 组装 Echo：管理 API（/api）和弹弹 API（/dandanplay，见 registerDandanRoutes）由 h 处理，其余路径托管前端。
func New(cfg config.Server, dandanplay config.Dandanplay, logger *slog.Logger, h *Handlers) *Server {
	return newServer(cfg, dandanplay, logger, h, web.FS())
}

// newServer 组装 Echo，ui 是要托管的前端文件，测试里换成假的文件系统。
func newServer(cfg config.Server, dandanplay config.Dandanplay, logger *slog.Logger, h *Handlers, ui fs.FS) *Server {
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
	registerDandanRoutes(e, dandanplay, h.Dandan)

	return &Server{cfg: cfg, logger: logger, echo: e}
}

// Start 启动 HTTP 服务并阻塞，ctx 取消后优雅退出。
func (s *Server) Start(ctx context.Context) error {
	sc := echo.StartConfig{
		Address:         s.cfg.Addr,
		HideBanner:      true,
		GracefulTimeout: s.cfg.GracefulTimeout,
		BeforeServeFunc: func(hs *http.Server) error {
			hs.ReadHeaderTimeout = readHeaderTimeout
			hs.IdleTimeout = idleTimeout
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
