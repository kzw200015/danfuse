package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
)

// errorHandler 全局错误处理：把任意 error 转换为统一响应结构，5xx 记 error 日志，4xx 不记录。
//   - *apierr.Error：按其 HTTP 状态码与业务码返回
//   - echo 框架错误（路由 404、405 等）：沿用框架给出的 HTTP 状态码，业务码为 CodeFail
//   - 其他错误：500 + CodeFail，不向客户端暴露细节（细节只进日志）
//
// 响应头已经输出（例如边读边写的导出中途出错）时响应改不成错误了：5xx 照常记日志，再中止连接（http.ErrAbortHandler，
// net/http 不记录它），客户端把这次请求标为失败，而不是收下一份看着正常、实际不完整的响应体。
// errorHandler 在 Recover 中间件之外调用，panic 直接到 net/http。
func errorHandler(c *echo.Context, err error) {
	appErr := toAppError(err)
	if appErr.HTTPStatus >= http.StatusInternalServerError {
		response.LogServerError(c, err)
	}
	if resp, _ := echo.UnwrapResponse(c.Response()); resp != nil && resp.Committed {
		panic(http.ErrAbortHandler)
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
