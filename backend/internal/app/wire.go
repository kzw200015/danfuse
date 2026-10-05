//go:build wireinject

package app

import (
	"context"

	"github.com/google/wire"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/dandan"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/handler"
	"github.com/kzw200015/danfuse/backend/internal/pkg/logger"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/server"
	"github.com/kzw200015/danfuse/backend/internal/service"
)

// Init 组装整个应用：解析配置 → 初始化日志 → 连接数据库并自动迁移 → 构造各层组件。
// 修改依赖关系后执行 `make wire` 重新生成 wire_gen.go。
func Init(ctx context.Context, configPath string) (*App, func(), error) {
	panic(wire.Build(
		config.Load,
		wire.FieldsOf(new(*config.Config), "Server", "Log", "Database", "Dandanplay", "CatalogSource", "Sync", "Bilibili"),
		logger.New,
		database.NewPool,
		repository.NewStore,
		wire.Bind(new(repository.Store), new(*repository.SQLStore)),
		newCatalogSource,
		newSourceRegistry,
		service.ProviderSet,
		newAggregator,
		handler.ProviderSet,
		dandan.NewHandler,
		server.New,
		New,
	))
}
