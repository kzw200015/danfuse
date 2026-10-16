package response

import (
	"log/slog"

	"github.com/labstack/echo/v5"
)

// LogServerError 按 error 级别记录一次服务端错误（5xx）：request_id、方法、路由和完整的错误链。
// 全局错误处理器写 5xx 响应时调用；自己写 5xx 响应、不经过它的 handler 也要调用。
// request_id 取自 RequestID 中间件写入的响应头；路由是注册时的路径模式（如 /api/series/:id），
// 不记录请求的实际 URL，资源 ID 等细节由错误链携带。
// 请求的 ctx 已经取消（客户端断开，例如关掉了正在等重新拉取的页面）时，错误多半就是取消本身，响应也发不出去，
// 不算服务端故障：改记 info 级别的 "request canceled"，字段相同。
func LogServerError(c *echo.Context, err error) {
	ctx := c.Request().Context()
	level, msg := slog.LevelError, "request failed"
	if ctx.Err() != nil {
		level, msg = slog.LevelInfo, "request canceled"
	}
	c.Logger().Log(ctx, level, msg,
		"request_id", c.Response().Header().Get(echo.HeaderXRequestID),
		"method", c.Request().Method,
		"route", c.Path(),
		"error", err,
	)
}
