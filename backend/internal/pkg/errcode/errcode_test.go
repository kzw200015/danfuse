package errcode_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
)

func TestIs(t *testing.T) {
	dbErr := errors.New("duplicate key")

	tests := []struct {
		name   string
		err    error
		target error
		want   bool
	}{
		{"WithMessage 派生", errcode.ErrUserNotFound.WithMessage("x"), errcode.ErrUserNotFound, true},
		{"Wrap 派生", errcode.ErrUserEmailExists.Wrap(dbErr), errcode.ErrUserEmailExists, true},
		{"Wrap 后仍能匹配底层错误", errcode.ErrUserEmailExists.Wrap(dbErr), dbErr, true},
		{"多次派生", errcode.ErrBadRequest.WithMessage("x").Wrap(dbErr), errcode.ErrBadRequest, true},
		{"被 fmt.Errorf 包装", fmt.Errorf("ctx: %w", errcode.ErrUserNotFound.WithMessage("x")), errcode.ErrUserNotFound, true},
		{"不同的通用错误", errcode.ErrBadRequest.Wrap(dbErr), errcode.ErrInternal, false},
		{"业务码与状态码相同但不是同一个定义", errcode.New(http.StatusBadRequest, errcode.CodeFail, "Bad Request"), errcode.ErrBadRequest, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := errors.Is(tt.err, tt.target); got != tt.want {
				t.Errorf("errors.Is() = %v, want %v", got, tt.want)
			}
		})
	}
}

// 全局错误处理器用 errors.AsType 取 *Error 输出响应，必须拿到最外层（最后一次派生）的错误。
func TestAsTypeReturnsOutermost(t *testing.T) {
	err := fmt.Errorf("ctx: %w", errcode.ErrBadRequest.WithMessage("姓名不能为空").Wrap(errors.New("cause")))

	appErr, ok := errors.AsType[*errcode.Error](err)
	if !ok {
		t.Fatal("errors.AsType() = false")
	}
	if appErr.Message != "姓名不能为空" {
		t.Errorf("Message = %q, want %q", appErr.Message, "姓名不能为空")
	}
}

func TestDeriveDoesNotMutateDefinition(t *testing.T) {
	_ = errcode.ErrBadRequest.WithMessage("x").Wrap(errors.New("cause"))

	if errcode.ErrBadRequest.Message != "请求参数错误" || len(errcode.ErrBadRequest.Unwrap()) != 0 {
		t.Errorf("预定义错误被修改: %v", errcode.ErrBadRequest)
	}
}
