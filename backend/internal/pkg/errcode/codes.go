package errcode

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
	ErrInternal           = New(http.StatusInternalServerError, CodeFail, "服务器内部错误")
	ErrServiceUnavailable = New(http.StatusServiceUnavailable, CodeFail, "服务暂不可用")
)

// 业务错误：只为前端需要分支处理（跳转、特殊交互等）的场景定义专门的业务码。
// 业务码全局唯一，按模块分段，每个模块占 1000 个号段。

// 用户模块 10001 ~ 10999
var (
	ErrUserNotFound    = New(http.StatusNotFound, 10001, "用户不存在")
	ErrUserEmailExists = New(http.StatusConflict, 10002, "邮箱已被使用")
)
