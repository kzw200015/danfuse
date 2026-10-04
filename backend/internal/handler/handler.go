// Package handler HTTP 处理层：解析参数、调用 service、输出统一响应。
// 出错时直接 return error，由全局错误处理器转换为统一响应。
package handler

import (
	"github.com/google/wire"
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
)

var ProviderSet = wire.NewSet(
	NewHealthHandler,
	NewSyncHandler,
	wire.Struct(new(Handlers), "*"),
)

// Handlers 汇总所有 handler，供路由注册使用。
type Handlers struct {
	Health *HealthHandler
	Sync   *SyncHandler
}

// validatable 约束请求类型：*T 必须实现 Validate，遇到第一个不合法的参数即返回错误。
// Validate 中也可以规整参数，例如去除首尾空格、为未传的参数填默认值。
type validatable[T any] interface {
	*T
	Validate() error
}

// bind 绑定请求参数（路径、查询、请求体）并校验，返回绑定好的请求：
//
//	req, err := bind[getSeriesRequest](c)
func bind[T any, P validatable[T]](c *echo.Context) (*T, error) {
	var req T
	if err := c.Bind(&req); err != nil {
		return nil, errcode.ErrBadRequest.Wrap(err)
	}
	if err := P(&req).Validate(); err != nil {
		return nil, err
	}
	return &req, nil
}

// invalidParam 构造参数校验失败的错误，message 会直接展示给用户。
func invalidParam(message string) error {
	return errcode.ErrBadRequest.WithMessage(message)
}
