package apierr

import "net/http"

const (
	// CodeOK 成功。
	CodeOK = 0
	// CodeFail 通用失败。没有专门业务码的错误（参数错误、框架错误、服务器内部错误等）统一使用，
	// 前端无需分支处理，直接提示 message 即可；HTTP 状态码仍保持各自语义。
	CodeFail = 1
)

// 通用错误：业务码均为 CodeFail，只有 HTTP 状态码与提示信息不同。
var (
	ErrBadRequest         = New(http.StatusBadRequest, CodeFail, "请求参数错误")
	ErrNotFound           = New(http.StatusNotFound, CodeFail, "资源不存在")
	ErrConflict           = New(http.StatusConflict, CodeFail, "操作冲突")
	ErrUnprocessable      = New(http.StatusUnprocessableEntity, CodeFail, "无法处理")
	ErrBadGateway         = New(http.StatusBadGateway, CodeFail, "上游服务异常")
	ErrInternal           = New(http.StatusInternalServerError, CodeFail, "服务器内部错误")
	ErrServiceUnavailable = New(http.StatusServiceUnavailable, CodeFail, "服务暂不可用")
)

// 业务错误：只为前端需要分支处理（跳转、特殊交互等）的场景定义专门的业务码，并同步到前端 src/api/apierr.ts。
// 业务码全局唯一，按模块分段，每个模块占 1000 个号段。目前没有业务码。
