// Package service 业务逻辑层。出错时返回 errcode.Error 表达业务语义，其他错误会被视为服务器内部错误。
package service

import "github.com/google/wire"

var ProviderSet = wire.NewSet(NewUserService)
