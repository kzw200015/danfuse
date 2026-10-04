// Package app 应用入口：通过 wire 组装依赖，负责启动流程。
// 配置解析、数据库连接与自动迁移都在依赖构造阶段（Init）完成，Run 只负责运行 HTTP 服务和后台同步。
package app

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/kzw200015/danfuse/backend/internal/server"
	"github.com/kzw200015/danfuse/backend/internal/service"
)

type App struct {
	server *server.Server
	sync   *service.SyncService
}

func New(srv *server.Server, sync *service.SyncService) *App {
	return &App{server: srv, sync: sync}
}

// Run 同时运行 HTTP 服务与后台同步并阻塞。ctx 取消时两者都退出：HTTP 服务优雅关闭，
// 进行中的同步记为 interrupted；HTTP 服务出错（例如端口被占用）时同步也随之退出。
// Run 等同步写完最终状态才返回，之后 wire 的 cleanup 才关闭连接池。
func (a *App) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return a.server.Start(ctx) })
	g.Go(func() error {
		a.sync.Run(ctx)
		return nil
	})
	return g.Wait()
}
