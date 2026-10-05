package server

import (
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/apierr"
	"github.com/kzw200015/danfuse/backend/internal/pkg/logger"
	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
)

// errorHandler 全局错误处理：把任意 error 转换为统一响应结构，5xx 记 error 日志，4xx 不记录。
//   - *apierr.Error：按其 HTTP 状态码与业务码返回
//   - echo 框架错误（路由 404、405 等）：沿用框架给出的 HTTP 状态码，业务码为 CodeFail
//   - 其他错误：500 + CodeFail，不向客户端暴露细节（细节只进日志）
func errorHandler(c *echo.Context, err error) {
	if resp, _ := echo.UnwrapResponse(c.Response()); resp != nil && resp.Committed {
		return
	}

	appErr := toAppError(err)
	if appErr.HTTPStatus >= http.StatusInternalServerError {
		logger.ServerError(c, err)
	}

	var writeErr error
	if c.Request().Method == http.MethodHead {
		writeErr = c.NoContent(appErr.HTTPStatus)
	} else {
		writeErr = response.Fail(c, appErr)
	}
	if writeErr != nil {
		c.Logger().Error("write error response failed", "error", writeErr)
	}
}

func toAppError(err error) *apierr.Error {
	if appErr, ok := errors.AsType[*apierr.Error](err); ok {
		return appErr
	}
	if status := echo.StatusCode(err); status != 0 {
		return apierr.New(status, apierr.CodeFail, http.StatusText(status))
	}
	return apierr.ErrInternal
}

// checkToken 弹弹 API 路径里的 token 不对时按路由不存在处理（404，交给全局 errorHandler）。
// 常数时间比较，不从响应时间泄露 token。
func checkToken(token string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if subtle.ConstantTimeCompare([]byte(c.Param("token")), []byte(token)) != 1 {
				return echo.ErrNotFound
			}
			return next(c)
		}
	}
}
