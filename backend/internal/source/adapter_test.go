package source

import "testing"

func TestCanonicalRef(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{"键按顺序排列", `{"p":2,"bvid":"BV1xx411c7XX"}`, `{"bvid":"BV1xx411c7XX","p":2}`},
		{"去掉 jsonb 输出的空格", `{"p": 2, "bvid": "BV1xx411c7XX"}`, `{"bvid":"BV1xx411c7XX","p":2}`},
		{"嵌套的对象也排序", `{"b": {"y": 1, "x": [3, 1]}, "a": null}`, `{"a":null,"b":{"x":[3,1],"y":1}}`},
		{"大整数保留原样", `{"cid": 9007199254740993}`, `{"cid":9007199254740993}`},
		{"字符串的写法统一", `{"t":"é<&>"}`, `{"t":"é<&>"}`},
		{"不是对象的 ref", ` "a" `, `"a"`},
		{"不是 JSON 时原样返回", `{"a":`, `{"a":`},
		{"后面跟着多余的内容时原样返回", `{"a":1} 2`, `{"a":1} 2`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanonicalRef(Ref(tt.ref)); got != tt.want {
				t.Errorf("CanonicalRef(%s) = %s, want %s", tt.ref, got, tt.want)
			}
		})
	}
}
