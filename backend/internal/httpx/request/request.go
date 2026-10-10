// Package request 绑定并校验请求参数。校验失败时返回 apierr.ErrBadRequest，由全局错误处理器转换为统一响应。
package request

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
)

// validatable 约束请求类型：*T 必须实现 Validate，遇到第一个不合法的参数即返回错误。
// Validate 中也可以规整参数，例如去除首尾空格、为未传的参数填默认值。
type validatable[T any] interface {
	*T
	Validate() error
}

// Bind 绑定请求参数（路径、查询、请求体）并校验，返回绑定好的请求：
//
//	req, err := request.Bind[getSeriesRequest](c)
func Bind[T any, P validatable[T]](c *echo.Context) (*T, error) {
	var req T
	if err := c.Bind(&req); err != nil {
		return nil, apierr.ErrBadRequest.Wrap(err)
	}
	if err := P(&req).Validate(); err != nil {
		return nil, err
	}
	return &req, nil
}

// InvalidParam 构造参数校验失败的错误，message 会直接展示给用户。
func InvalidParam(message string) error {
	return apierr.ErrBadRequest.WithMessage(message)
}
