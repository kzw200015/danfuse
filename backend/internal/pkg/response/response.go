// Package response 定义统一响应结构：
//
//	{"code": 0, "message": "ok", "data": {...}}
//
// code 为 0 表示成功，非 0 为业务错误码（见 errcode 包）。
package response

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
)

type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// Page 分页数据，作为 Response.Data 返回。
type Page[T any] struct {
	List     []T   `json:"list"`
	Total    int64 `json:"total"`
	Page     int32 `json:"page"`
	PageSize int32 `json:"pageSize"`
}

// NewPage 构造分页数据，list 为 nil 时输出空数组而不是 null。
func NewPage[T any](list []T, total int64, page, pageSize int32) Page[T] {
	if list == nil {
		list = []T{}
	}
	return Page[T]{List: list, Total: total, Page: page, PageSize: pageSize}
}

// OK 返回 200 成功响应。
func OK(c *echo.Context, data any) error {
	return c.JSON(http.StatusOK, Response{Code: errcode.CodeOK, Message: "ok", Data: data})
}

// Created 返回 201 成功响应。
func Created(c *echo.Context, data any) error {
	return c.JSON(http.StatusCreated, Response{Code: errcode.CodeOK, Message: "ok", Data: data})
}

// Accepted 返回 202 成功响应：请求已受理，在后台处理。
func Accepted(c *echo.Context, data any) error {
	return c.JSON(http.StatusAccepted, Response{Code: errcode.CodeOK, Message: "ok", Data: data})
}

// Fail 返回错误响应。handler 中一般直接 return 错误，由全局错误处理器调用本函数。
func Fail(c *echo.Context, e *errcode.Error) error {
	return c.JSON(e.HTTPStatus, Response{Code: e.Code, Message: e.Message})
}
