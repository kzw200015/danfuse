// Package app 组装应用并运行。New 只构造对象，不做 IO：配置、日志、连接池与迁移由 main 准备好再传进来，
// 构造出的组件都不连外部系统；Run 运行 HTTP 服务、后台同步与补建。
package app

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/catalog/jellyfin"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/dandan"
	"github.com/kzw200015/danfuse/backend/internal/handler"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/server"
	"github.com/kzw200015/danfuse/backend/internal/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/source/bilibili"
)

type App struct {
	server         *server.Server
	sync           *service.SyncService
	seasonBindings *service.SeasonBindingService
}

// New 组装各层组件。pool 已连通、已迁移，由调用方在 Run 返回之后关闭。
func New(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool) *App {
	store := repository.NewStore(pool)

	// 适配器子包只由 app 引用，业务代码只依赖领域包的接口。
	// B 站适配器里有全局令牌桶，整个进程只构造这一个。
	sources := source.NewRegistry(bilibili.New(cfg.Bilibili, logger))
	catalogSource := newCatalogSource(cfg.CatalogSource)

	catalogs := service.NewCatalogService(store, sources)
	bindings := service.NewBindingService(store, sources, logger)
	seasonBindings := service.NewSeasonBindingService(store, pool, sources, bindings, logger)
	syncs := service.NewSyncService(store, pool, catalogSource, cfg.Sync, logger)
	dandanService := service.NewDandanService(store, sources)

	handlers := &handler.Handlers{
		Health:        handler.NewHealthHandler(pool),
		Catalog:       handler.NewCatalogHandler(catalogs),
		Binding:       handler.NewBindingHandler(bindings, cfg.DanmakuFile),
		SeasonBinding: handler.NewSeasonBindingHandler(seasonBindings),
		Sync:          handler.NewSyncHandler(syncs),
		Settings:      handler.NewSettingsHandler(cfg.Dandanplay, cfg.CatalogSource, cfg.Sync, cfg.Bilibili),
	}
	return &App{
		server:         server.New(cfg.Server, cfg.Dandanplay, logger, handlers, dandan.NewHandler(dandanService)),
		sync:           syncs,
		seasonBindings: seasonBindings,
	}
}

// newCatalogSource 按 kind 选择目录源适配；kind 为空时返回 nil，表示未配置目录源。
func newCatalogSource(cfg config.CatalogSource) catalog.Source {
	switch cfg.Kind {
	case config.KindJellyfin:
		return jellyfin.New(cfg.Jellyfin)
	default: // config 已校验，只能为空
		return nil
	}
}

// Run 同时运行 HTTP 服务、后台同步与季绑定的补建（追更的扫描）并阻塞。ctx 取消时都退出：HTTP 服务优雅关闭，
// 进行中的同步记为 interrupted，进行中的补建停下（不写上次检查时间，重启后接着做）；
// HTTP 服务出错（例如端口被占用）时同步与补建也随之退出。
// Run 等同步写完最终状态、补建停下才返回，调用方之后才能关闭连接池。
func (a *App) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return a.server.Start(ctx) })
	g.Go(func() error {
		a.sync.Run(ctx)
		return nil
	})
	g.Go(func() error {
		a.seasonBindings.Run(ctx)
		return nil
	})
	return g.Wait()
}
