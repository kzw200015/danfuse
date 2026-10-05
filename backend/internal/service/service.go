// Package service 业务逻辑层。出错时返回 errcode.Error 表达业务语义，其他错误会被视为服务器内部错误。
// 所有读写数据库的业务都在这一层；catalog 等领域包只有接口、类型、纯计算和外部适配。
package service

import "github.com/google/wire"

var ProviderSet = wire.NewSet(
	NewCatalogService,
	NewBindingService,
	NewSyncService,
	NewSeasonBindingService,
	NewLocalProvider,
)

// 以下为列与 Go 类型之间的转换，同步写入与本地 Provider 读取共用。

// nullIfEmpty 空串存为 null。
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// emptyIfNull null 读作空串。
func emptyIfNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	return new(int32(*v))
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	return new(int(*v))
}
