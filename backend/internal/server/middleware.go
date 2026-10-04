package server

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
)

// errorHandler 全局错误处理：把任意 error 转换为统一响应结构。
//   - *errcode.Error：按其 HTTP 状态码与业务码返回
//   - echo 框架错误（路由 404、405 等）：沿用框架给出的 HTTP 状态码，业务码为 CodeFail
//   - 其他错误：500 + CodeFail，不向客户端暴露细节（细节由请求日志记录）
func errorHandler(c *echo.Context, err error) {
	if resp, _ := echo.UnwrapResponse(c.Response()); resp != nil && resp.Committed {
		return
	}

	appErr := toAppError(err)

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

// requestLogger 记录每个请求；错误交给全局错误处理器后再记录，保证日志中的状态码与实际响应一致。
func requestLogger(logger *slog.Logger) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		HandleError:  true,
		LogLatency:   true,
		LogRemoteIP:  true,
		LogMethod:    true,
		LogURI:       true,
		LogRequestID: true,
		LogStatus:    true,
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			level := slog.LevelInfo
			switch {
			case v.Status >= http.StatusInternalServerError:
				level = slog.LevelError
			case v.Status >= http.StatusBadRequest:
				level = slog.LevelWarn
			}

			attrs := []slog.Attr{
				slog.String("method", v.Method),
				slog.String("uri", v.URI),
				slog.Int("status", v.Status),
				slog.Duration("latency", v.Latency),
				slog.String("remote_ip", v.RemoteIP),
				slog.String("request_id", v.RequestID),
			}
			if v.Error != nil {
				attrs = append(attrs, slog.String("error", v.Error.Error()))
			}
			logger.LogAttrs(c.Request().Context(), level, "request", attrs...)
			return nil
		},
	})
}
