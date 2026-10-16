package source

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// fakeAdapter 什么链接都不认识的适配器。
type fakeAdapter struct{ id string }

var _ Adapter = (*fakeAdapter)(nil)

func (a *fakeAdapter) ID() string                 { return a.id }
func (a *fakeAdapter) Platform() danmaku.Platform { return danmaku.PlatformNone }
func (a *fakeAdapter) Describe(Ref) (Display, error) {
	return Display{}, nil
}

func (a *fakeAdapter) Fetch(context.Context, Ref) (Fetched, error) {
	return Fetched{}, nil
}

func (a *fakeAdapter) ParseLink(context.Context, string) (Ref, error) {
	return nil, ErrUnrecognized
}

func (a *fakeAdapter) ParseCollectionLink(context.Context, string) ([]CollectionCandidate, error) {
	return nil, ErrUnrecognized
}

func (a *fakeAdapter) ListCollection(context.Context, CollectionRef) (Collection, error) {
	return Collection{}, nil
}

func (a *fakeAdapter) DescribeCollection(CollectionRef) (Display, error) {
	return Display{}, nil
}

// fakeLinker 认识以 prefix 开头的链接，弹幕源与合集的链接规则相同；链接里带 "bad" 时返回 InvalidLink。
type fakeLinker struct {
	fakeAdapter
	prefix string
	asked  int
}

var _ Adapter = (*fakeLinker)(nil)

func newFakeLinker(id string) *fakeLinker {
	return &fakeLinker{id: id, prefix: id + ":"}
}

// parse 两种链接共用的规则，返回 ref 的内容。
func (l *fakeLinker) parse(link string) (string, error) {
	l.asked++
	switch {
	case !strings.HasPrefix(link, l.prefix):
		return "", ErrUnrecognized
	case strings.Contains(link, "bad"):
		return "", &Error{Kind: InvalidLink, Message: "这个链接不能绑定"}
	}
	return `"` + strings.TrimPrefix(link, l.prefix) + `"`, nil
}

func (l *fakeLinker) ParseLink(_ context.Context, link string) (Ref, error) {
	ref, err := l.parse(link)
	if err != nil {
		return nil, err
	}
	return Ref(ref), nil
}

func (l *fakeLinker) ParseCollectionLink(_ context.Context, link string) ([]CollectionCandidate, error) {
	ref, err := l.parse(link)
	if err != nil {
		return nil, err
	}
	return []CollectionCandidate{{Kind: "list", Ref: CollectionRef(ref)}}, nil
}

func TestRegistryGet(t *testing.T) {
	plain, linker := &fakeAdapter{id: "file"}, newFakeLinker("site")
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

// TestRegistryParse ParseLink 与 ParseCollectionLink 询问各适配器的规则相同，每个用例两个方法都测。
func TestRegistryParse(t *testing.T) {
	// 两个方法都返回认出链接的适配器和 ref；合集取唯一的那个候选的 ref
	methods := map[string]func(ctx context.Context, r *Registry, link string) (Adapter, string, error){
		"ParseLink": func(ctx context.Context, r *Registry, link string) (Adapter, string, error) {
			adapter, ref, err := r.ParseLink(ctx, link)
			return adapter, string(ref), err
		},
		"ParseCollectionLink": func(ctx context.Context, r *Registry, link string) (Adapter, string, error) {
			adapter, candidates, err := r.ParseCollectionLink(ctx, link)
			switch len(candidates) {
			case 0:
				return adapter, "", err
			case 1:
				return adapter, string(candidates[0].Ref), err
			}
			return nil, "", errors.New("候选不止一个")
		},
	}
	tests := []struct {
		name        string
		link        string
		wantAdapter string // 为空表示期望出错
		wantRef     string
		wantMessage string
		wantAsked   [2]int // 两个适配器各被问了几次
	}{
		{name: "第一个适配器认识", link: "a:1", wantAdapter: "a", wantRef: `"1"`, wantAsked: [2]int{1, 0}},
		{name: "第一个不认识时交给下一个", link: "b:2", wantAdapter: "b", wantRef: `"2"`, wantAsked: [2]int{1, 1}},
		{name: "都不认识", link: "c:3", wantMessage: "无法识别的链接", wantAsked: [2]int{1, 1}},
		{name: "认识但不能绑定：直接返回适配器的错误", link: "a:bad", wantMessage: "这个链接不能绑定", wantAsked: [2]int{1, 0}},
	}
	for method, parse := range methods {
		for _, tt := range tests {
			t.Run(method+"/"+tt.name, func(t *testing.T) {
				a, b := newFakeLinker("a"), newFakeLinker("b")
				r := NewRegistry(&fakeAdapter{id: "file"}, a, b)

				adapter, ref, err := parse(t.Context(), r, tt.link)

				if tt.wantAdapter != "" {
					if err != nil || adapter == nil || adapter.ID() != tt.wantAdapter || ref != tt.wantRef {
						t.Errorf("%s() = (%v, %s, %v), want (%s, %s, nil)", method, adapter, ref, err, tt.wantAdapter, tt.wantRef)
					}
				} else {
					srcErr, ok := errors.AsType[*Error](err)
					if !ok || srcErr.Kind != InvalidLink || srcErr.Message != tt.wantMessage || adapter != nil || ref != "" {
						t.Errorf("%s() = (%v, %s, %v), want InvalidLink %q", method, adapter, ref, err, tt.wantMessage)
					}
				}
				if got := [2]int{a.asked, b.asked}; got != tt.wantAsked {
					t.Errorf("询问次数 = %v, want %v", got, tt.wantAsked)
				}
			})
		}
	}
}
