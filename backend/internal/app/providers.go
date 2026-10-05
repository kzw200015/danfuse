package app

import (
	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/catalog/jellyfin"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/provider"
	"github.com/kzw200015/danfuse/backend/internal/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/source/bilibili"
)

// newCatalogSource 按 kind 选择目录源适配；kind 为空时返回 nil，表示未配置目录源。
// 适配器子包只由 app 引用，业务代码只依赖 catalog.Source 接口。
func newCatalogSource(cfg config.CatalogSource) catalog.Source {
	switch cfg.Kind {
	case config.KindJellyfin:
		return jellyfin.New(cfg.Jellyfin)
	default: // config 已校验，只能为空
		return nil
	}
}

// newSourceRegistry 注册所有源适配器，业务代码只依赖 source 包的接口。
// B 站适配器里有全局令牌桶，整个进程只构造这一个。
func newSourceRegistry(cfg config.Bilibili) *source.Registry {
	return source.NewRegistry(bilibili.New(cfg))
}

// newAggregator 弹弹 API 用的聚合层，现在只注册了本地 Provider。
func newAggregator(local *service.LocalProvider) *provider.Aggregator {
	return provider.NewAggregator(local)
}
