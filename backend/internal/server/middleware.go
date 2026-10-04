package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/pkg/logger"
	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
)

// errorHandler 全局错误处理：把任意 error 转换为统一响应结构，5xx 记 error 日志，4xx 不记录。
//   - *errcode.Error：按其 HTTP 状态码与业务码返回
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

func toAppError(err error) *errcode.Error {
	if appErr, ok := errors.AsType[*errcode.Error](err); ok {
		return appErr
	}
	if status := echo.StatusCode(err); status != 0 {
		return errcode.New(status, errcode.CodeFail, http.StatusText(status))
	}
	return errcode.ErrInternal
}
