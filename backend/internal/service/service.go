// Package service 业务逻辑层。出错时返回 errcode.Error 表达业务语义，其他错误会被视为服务器内部错误。
// 所有读写数据库的业务都在这一层；catalog 等领域包只有接口、类型、纯计算和外部适配。
package service

import "github.com/google/wire"

var ProviderSet = wire.NewSet(
	NewCatalogService,
	NewSyncService,
)
