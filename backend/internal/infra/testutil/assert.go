// Package testutil 测试用的断言，与业务无关；组装各领域的 service、假适配器在 testenv。
package testutil

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
)

// AssertAppError err 是给定状态码与提示的 *apierr.Error。
func AssertAppError(t testing.TB, err error, status int, message string) {
	t.Helper()
	appErr, ok := errors.AsType[*apierr.Error](err)
	if !ok || appErr.HTTPStatus != status || appErr.Message != message {
		t.Errorf("err = %v, want %d %q", err, status, message)
	}
}

// AssertStrings 逐项比较两组字符串，what 写进失败信息。
func AssertStrings(t testing.TB, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %q\nwant %q", what, got, want)
	}
}

// AssertJSON 把 got 编码成 JSON，与 want 按语义比较：字段名与值都要一致，不管字段顺序与空白。
// got 是 []byte 或 json.RawMessage 时当作已经编码好的 JSON（例如响应体）。
func AssertJSON(t testing.TB, got any, want string) {
	t.Helper()
	var b []byte
	switch v := got.(type) {
	case []byte:
		b = v
	case json.RawMessage:
		b = v
	default:
		var err error
		if b, err = json.Marshal(got); err != nil {
			t.Fatal(err)
		}
	}
	var g, w any
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("got 不是 JSON：%s", b)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("期望值不是 JSON：%v", err)
	}
	if !reflect.DeepEqual(g, w) {
		// 两边都重新编码，字段按名称排序，方便对照
		gb, _ := json.Marshal(g)
		wb, _ := json.Marshal(w)
		t.Errorf("got  %s\nwant %s", gb, wb)
	}
}
