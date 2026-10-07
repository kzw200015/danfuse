// Package response 定义统一响应结构：
//
//	{"code": 0, "message": "ok", "data": {...}}
//
// code 为 0 表示成功，非 0 为业务错误码（见 apierr 包）。
package response

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/apierr"
)

type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// OK 返回 200 成功响应。
func OK(c *echo.Context, data any) error { return success(c, http.StatusOK, data) }

// Created 返回 201 成功响应。
func Created(c *echo.Context, data any) error { return success(c, http.StatusCreated, data) }

// Accepted 返回 202 成功响应：请求已受理，在后台处理。
func Accepted(c *echo.Context, data any) error { return success(c, http.StatusAccepted, data) }

func success(c *echo.Context, status int, data any) error {
	return c.JSON(status, Response{Code: apierr.CodeOK, Message: "ok", Data: data})
}

// Fail 返回错误响应。handler 中一般直接 return 错误，由全局错误处理器调用本函数。
func Fail(c *echo.Context, e *apierr.Error) error {
	return c.JSON(e.HTTPStatus, Response{Code: e.Code, Message: e.Message})
}
