package source

import (
	"errors"
	"fmt"
)

// Kind 适配器错误的类别，调用方据此决定绑定的状态和 HTTP 状态码。
type Kind int

const (
	InvalidLink  Kind = iota + 1 // 链接无法识别，或能识别但不能绑定
	NotFound                     // 弹幕源不存在、已删除或不可见；重新拉取时绑定标为失效
	AuthRequired                 // 需要登录
	RateLimited                  // 平台限流
	Upstream                     // 平台故障、超时、响应无法解析
)

// String 日志里的名称。
func (k Kind) String() string {
	switch k {
	case InvalidLink:
		return "invalid_link"
	case NotFound:
		return "not_found"
	case AuthRequired:
		return "auth_required"
	case RateLimited:
		return "rate_limited"
	case Upstream:
		return "upstream"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Error 适配器返回的错误。Message 是给用户看的提示，由适配器写，因为提示与平台有关
// （例如"B 站限流，请稍后再试"）；Err 是底层原因（平台的错误码等），只进日志。
// 调用方用 errors.AsType 取出。
type Error struct {
	Kind    Kind
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// ErrUnrecognized 适配器不认识这个链接（ParseLink、ParseCollectionLink），Registry 会接着交给下一个适配器。
var ErrUnrecognized = errors.New("source: unrecognized link")
