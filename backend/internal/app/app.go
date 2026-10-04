// Package app 应用入口：通过 wire 组装依赖，负责启动流程。
// 配置解析、数据库连接与自动迁移都在依赖构造阶段（Init）完成，Run 只负责运行服务。
package app

import (
	"context"

	"github.com/kzw200015/danfuse/backend/internal/server"
)

type App struct {
	server *server.Server
}

func New(srv *server.Server) *App {
	return &App{server: srv}
}

// Run 启动 HTTP 服务并阻塞，ctx 取消时优雅退出。
func (a *App) Run(ctx context.Context) error {
	return a.server.Start(ctx)
}
