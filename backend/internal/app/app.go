// Package app 组装应用并运行。New 只构造对象，不做 IO：配置、日志、连接池与迁移由 main 准备好再传进来，
// 构造出的组件都不连外部系统；Run 运行 HTTP 服务、后台同步、补建与定时拉取。
package app

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	bindinghandler "github.com/kzw200015/danfuse/backend/internal/binding/handler"
	bindingsvc "github.com/kzw200015/danfuse/backend/internal/binding/service"
	blockwordhandler "github.com/kzw200015/danfuse/backend/internal/blockword/handler"
	blockwordsvc "github.com/kzw200015/danfuse/backend/internal/blockword/service"
	"github.com/kzw200015/danfuse/backend/internal/catalog"
	cataloghandler "github.com/kzw200015/danfuse/backend/internal/catalog/handler"
	"github.com/kzw200015/danfuse/backend/internal/catalog/jellyfin"
	catalogsvc "github.com/kzw200015/danfuse/backend/internal/catalog/service"
	dandanhandler "github.com/kzw200015/danfuse/backend/internal/dandan/handler"
	dandansvc "github.com/kzw200015/danfuse/backend/internal/dandan/service"
	healthhandler "github.com/kzw200015/danfuse/backend/internal/health/handler"
	healthsvc "github.com/kzw200015/danfuse/backend/internal/health/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	seasonbindinghandler "github.com/kzw200015/danfuse/backend/internal/seasonbinding/handler"
	seasonbindingsvc "github.com/kzw200015/danfuse/backend/internal/seasonbinding/service"
	"github.com/kzw200015/danfuse/backend/internal/server"
	settingshandler "github.com/kzw200015/danfuse/backend/internal/settings/handler"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/source/bilibili"
)

// backgroundRunner 后台循环：实现 Run(ctx) 的 service，阻塞到 ctx 取消。
type backgroundRunner interface {
	Run(ctx context.Context)
}

type App struct {
	server *server.Server
	// background 与 HTTP 服务一起运行的后台循环：同步、季绑定的补建（追更的扫描）、定时拉取。
	// Run 在 ctx 取消后收尾完才返回。
	background []backgroundRunner
}

// New 组装各领域的 service 和 handler。pool 已连通、已迁移，由调用方在 Run 返回之后关闭。
// 领域之间的依赖：catalog → seasonbinding → binding，dandan → binding、blockword；同步（catalog.SyncService）独立。
func New(cfg *config.Config, logger *slog.Logger, pool *pgxpool.Pool) *App {
	// 适配器子包只由 app 引用，业务代码只依赖领域包的接口。
	// B 站适配器里有全局令牌桶，整个进程只构造这一个。
	sources := source.NewRegistry(bilibili.New(cfg.Bilibili, logger))
	catalogSource := newCatalogSource(cfg.CatalogSource)

	bindings := bindingsvc.NewService(pool, sources, logger)
	scheduledFetch := bindingsvc.NewScheduledFetchService(pool, bindings, cfg.ScheduledFetch, logger)
	seasonBindings := seasonbindingsvc.NewService(pool, sources, bindings, cfg.Follow, logger)
	catalogs := catalogsvc.NewService(pool, catalogSource, bindings, seasonBindings)
	syncs := catalogsvc.NewSyncService(pool, catalogSource, cfg.Sync, logger)
	blockedWords := blockwordsvc.NewService(pool)
	dandans := dandansvc.NewService(pool, bindings, blockedWords)

	handlers := &server.Handlers{
		Health:        healthhandler.NewHandler(healthsvc.NewService(pool)),
		Settings:      settingshandler.NewHandler(cfg),
		Catalog:       cataloghandler.NewHandler(catalogs),
		Sync:          cataloghandler.NewSyncHandler(syncs),
		Binding:       bindinghandler.NewHandler(bindings, cfg.DanmakuFile),
		SeasonBinding: seasonbindinghandler.NewHandler(seasonBindings, cfg.DanmakuFile),
		BlockedWord:   blockwordhandler.NewHandler(blockedWords),
		Dandan:        dandanhandler.NewHandler(dandans, cfg.Dandanplay),
	}
	return &App{
		server:     server.New(cfg.Server, logger, handlers),
		background: []backgroundRunner{syncs, seasonBindings, scheduledFetch},
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

// Run 同时运行 HTTP 服务、后台同步、季绑定的补建（追更的扫描）与定时拉取并阻塞。ctx 取消时都退出：HTTP 服务优雅关闭，
// 进行中的同步记为 interrupted，进行中的补建停下（不写上次检查时间，重启后接着做），进行中的定时拉取停下；
// HTTP 服务出错（例如端口被占用）时后台任务也随之退出。
// Run 等同步写完最终状态、补建与定时拉取停下才返回，调用方之后才能关闭连接池。
func (a *App) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return a.server.Start(ctx) })
	for _, bg := range a.background {
		g.Go(func() error {
			bg.Run(ctx)
			return nil
		})
	}
	return g.Wait()
}
