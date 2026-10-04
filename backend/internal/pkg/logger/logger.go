// Package logger 基于标准库 slog 构建全局日志，并统一服务端错误的日志字段。
package logger

import (
	"log/slog"
	"os"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/config"
)

// New 根据配置创建 logger，并设置为 slog 默认 logger。
func New(cfg config.Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level)}

	var h slog.Handler
	if strings.EqualFold(cfg.Format, "json") {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}

	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

func parseLevel(s string) slog.Level {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return lvl
}

// ServerError 按 error 级别记录一次服务端错误（5xx）：request_id、方法、路由和完整的错误链。
// request_id 取自 RequestID 中间件写入的响应头；路由是注册时的路径模式（如 /api/series/:id），
// 不记录请求的实际 URL，资源 ID 等细节由错误链携带。
func ServerError(c *echo.Context, err error) {
	c.Logger().Error("request failed",
		"request_id", c.Response().Header().Get(echo.HeaderXRequestID),
		"method", c.Request().Method,
		"route", c.Path(),
		"error", err,
	)
}
