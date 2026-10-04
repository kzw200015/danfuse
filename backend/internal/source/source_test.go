package source

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// fakeAdapter 只有 Adapter 的能力。
type fakeAdapter struct{ id string }

func (a *fakeAdapter) ID() string                 { return a.id }
func (a *fakeAdapter) Platform() danmaku.Platform { return danmaku.PlatformNone }
func (a *fakeAdapter) Describe(Ref) (Display, error) {
	return Display{}, nil
}

func (a *fakeAdapter) Fetch(context.Context, Ref) (Fetched, error) {
	return Fetched{}, nil
}

// fakeLinker 认识以 prefix 开头的链接；链接里带 "bad" 时返回 InvalidLink。
type fakeLinker struct {
	fakeAdapter
	prefix string
	asked  int
}

func (l *fakeLinker) ParseLink(_ context.Context, link string) (Ref, error) {
	l.asked++
	switch {
	case !strings.HasPrefix(link, l.prefix):
		return nil, ErrUnrecognized
	case strings.Contains(link, "bad"):
		return nil, &Error{Kind: InvalidLink, Message: "请打开具体某一集再复制链接"}
	}
	return Ref(`"` + strings.TrimPrefix(link, l.prefix) + `"`), nil
}

func TestRegistryGet(t *testing.T) {
	plain, linker := &fakeAdapter{id: "file"}, &fakeLinker{id: "site"}
	r := NewRegistry(plain, linker)

	for _, want := range []Adapter{plain, linker} {
		if got, err := r.Get(want.ID()); err != nil || got != want {
			t.Errorf("Get(%q) = (%v, %v), want (%v, nil)", want.ID(), got, err, want)
		}
	}
	if got, err := r.Get("other"); err == nil || !strings.Contains(err.Error(), `"other"`) || got != nil {
		t.Errorf("Get(未注册的 ID) = (%v, %v), want (nil, 指出 other 的错误)", got, err)
	}
}

func TestRegistryParseLink(t *testing.T) {
	tests := []struct {
		name        string
		link        string
		wantAdapter string // 为空表示期望出错
		wantRef     string
		wantMessage string
		wantAsked   [2]int // 两个 Linker 各被问了几次
	}{
		{name: "第一个 Linker 认识", link: "a:1", wantAdapter: "a", wantRef: `"1"`, wantAsked: [2]int{1, 0}},
		{name: "第一个不认识时交给下一个", link: "b:2", wantAdapter: "b", wantRef: `"2"`, wantAsked: [2]int{1, 1}},
		{name: "都不认识", link: "c:3", wantMessage: "无法识别的链接", wantAsked: [2]int{1, 1}},
		{name: "认识但不能绑定：直接返回适配器的错误", link: "a:bad", wantMessage: "请打开具体某一集再复制链接", wantAsked: [2]int{1, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &fakeLinker{id: "a", prefix: "a:"}
			b := &fakeLinker{id: "b", prefix: "b:"}
			r := NewRegistry(&fakeAdapter{id: "file"}, a, b)

			adapter, ref, err := r.ParseLink(t.Context(), tt.link)

			if tt.wantAdapter != "" {
				if err != nil || adapter.ID() != tt.wantAdapter || string(ref) != tt.wantRef {
					t.Errorf("ParseLink() = (%v, %s, %v), want (%s, %s, nil)", adapter, ref, err, tt.wantAdapter, tt.wantRef)
				}
			} else {
				srcErr, ok := errors.AsType[*Error](err)
				if !ok || srcErr.Kind != InvalidLink || srcErr.Message != tt.wantMessage || adapter != nil || ref != nil {
					t.Errorf("ParseLink() = (%v, %s, %v), want InvalidLink %q", adapter, ref, err, tt.wantMessage)
				}
			}
			if got := [2]int{a.asked, b.asked}; got != tt.wantAsked {
				t.Errorf("询问次数 = %v, want %v", got, tt.wantAsked)
			}
		})
	}
}
