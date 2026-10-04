package app

import (
	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/catalog/jellyfin"
	"github.com/kzw200015/danfuse/backend/internal/config"
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
