// Package logger 基于标准库 slog 构建全局日志。
package logger

import (
	"log/slog"
	"os"
	"strings"

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
