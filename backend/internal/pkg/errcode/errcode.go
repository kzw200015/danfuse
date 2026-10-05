// Package errcode 定义业务错误。service 层返回 *Error，由 HTTP 错误处理器统一转换成响应。
package errcode

import "fmt"

// Error 接口错误，携带 HTTP 状态码、业务码与提示信息。
//
// WithMessage / Wrap 不修改原错误，而是派生出新错误，并通过 Unwrap 形成两条分支：
//
//	派生错误 ─┬─> parent：派生来源，errors.Is 可匹配到原始的预定义错误
//	          └─> cause ：底层原因，errors.Is / errors.AsType 可匹配到底层错误
type Error struct {
	HTTPStatus int
	Code       int
	Message    string
	parent     *Error
	cause      error // 只用于日志，不会返回给客户端
}

func New(httpStatus, code int, message string) *Error {
	return &Error{HTTPStatus: httpStatus, Code: code, Message: message}
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("[%d] %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// Unwrap 返回派生来源与底层原因，errors.Is / errors.AsType 会依次沿两条分支查找。
func (e *Error) Unwrap() []error {
	errs := make([]error, 0, 2)
	if e.parent != nil {
		errs = append(errs, e.parent)
	}
	if e.cause != nil {
		errs = append(errs, e.cause)
	}
	return errs
}

// StatusCode 实现 echo.HTTPStatusCoder，使 echo 的中间件能识别出正确的 HTTP 状态码。
func (e *Error) StatusCode() int { return e.HTTPStatus }

// WithMessage 派生一个替换了提示信息的错误，保留原有的底层原因。
func (e *Error) WithMessage(message string) *Error {
	return &Error{HTTPStatus: e.HTTPStatus, Code: e.Code, Message: message, parent: e, cause: e.cause}
}

// Wrap 派生一个携带底层原因的错误。
func (e *Error) Wrap(err error) *Error {
	return &Error{HTTPStatus: e.HTTPStatus, Code: e.Code, Message: e.Message, parent: e, cause: err}
}
