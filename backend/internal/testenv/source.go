package testenv

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// FakeRef 假适配器的弹幕源 ref：{"name":"<名字>"}。
type FakeRef struct {
	Name string `json:"name"`
}

// DescribeFake 假适配器共用的弹幕源链接和标签。
func DescribeFake(ref source.Ref) (source.Display, error) {
	var r FakeRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Display{}, err
	}
	return source.Display{URL: "https://fake.test/" + r.Name, Label: "假弹幕源 " + r.Name}, nil
}

// UpstreamErr 与真实的适配器一样：ctx 结束（超时、取消）时按 Upstream 失败。
func UpstreamErr(ctx context.Context) error {
	return &source.Error{Kind: source.Upstream, Message: "B 站接口异常", Err: ctx.Err()}
}

// FakeVideos 每个名字一个弹幕源，各两条弹幕。
func FakeVideos(names ...string) map[string]source.Fetched {
	videos := make(map[string]source.Fetched, len(names))
	for _, name := range names {
		videos[name] = source.Fetched{Title: "弹幕源 " + name, Duration: 1420, Danmaku: []danmaku.Danmaku{
			{SourceID: 1, TimeMs: 0, Mode: danmaku.ModeScroll, Text: name + " 前排"},
			{SourceID: 2, TimeMs: 1500, Mode: danmaku.ModeTop, Color: 0xFFFFFF, Text: name + " 来了"},
		}}
	}
	return videos
}

// FakeCollector 有合集的假适配器，不联网，ID 为 fake；集面板的链接都不认识。
//   - 合集链接 "list/<名字>" 与 "alias-list/<名字>" 是同一个合集的两种写法，识别为一个种类为 list 的候选 {"list":"<名字>"}；
//     "both/<名字>" 识别为两个候选：种类 pages 的 {"pages":"<名字>"} 在前，种类 list 的 {"list":"<名字>"} 在后；
//     "series/<名字>" 为 InvalidLink"暂不支持系列"；其他链接认不出。
//   - ListCollection 按名字返回 Collections 里的合集（两种候选都按名字找），没有时为 NotFound；ListErr 不为 nil 时一律返回它。
//     ListStarted 不为 nil 时，ListCollection 先在 ListStarted 上报到，再等 ListRelease 关闭。
//   - 条目的弹幕源 ref 为 FakeRef，Fetch 返回 Videos 里这个名字的结果，没有时为 NotFound；FetchErrs 里有这个名字时返回那个错误。
//     Gate 后，Fetch 先在 Started 上报自己的名字，再从 Release 收到一次放行才继续；ctx 结束时与真实的适配器一样按 Upstream 失败。
//
// 测试在后台补建停下来（synctest.Wait）之后才改它的字段；后台读写的计数和记录有锁保护。
type FakeCollector struct {
	Collections map[string]source.Collection
	ListErr     error
	Videos      map[string]source.Fetched
	FetchErrs   map[string]error

	Started     chan string
	Release     chan struct{}
	ListStarted chan struct{}
	ListRelease chan struct{}

	mu      sync.Mutex
	lists   int      // ListCollection 被调用的次数
	fetched []string // Fetch 过的弹幕源名字，按调用顺序
}

var _ source.Adapter = (*FakeCollector)(nil)

type fakeCollectionRef struct {
	List  string `json:"list,omitempty"`
	Pages string `json:"pages,omitempty"`
}

func (f *FakeCollector) ID() string                 { return "fake" }
func (f *FakeCollector) Platform() danmaku.Platform { return danmaku.PlatformNone }

func (f *FakeCollector) Describe(ref source.Ref) (source.Display, error) { return DescribeFake(ref) }

func (f *FakeCollector) ParseLink(context.Context, string) (source.Ref, error) {
	return nil, source.ErrUnrecognized
}

func (f *FakeCollector) Fetch(ctx context.Context, ref source.Ref) (source.Fetched, error) {
	var r FakeRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Fetched{}, err
	}
	f.mu.Lock()
	f.fetched = append(f.fetched, r.Name)
	f.mu.Unlock()
	if f.Started != nil {
		select {
		case f.Started <- r.Name:
		case <-ctx.Done():
			return source.Fetched{}, UpstreamErr(ctx)
		}
		select {
		case <-f.Release:
		case <-ctx.Done():
			return source.Fetched{}, UpstreamErr(ctx)
		}
	}
	if err := f.FetchErrs[r.Name]; err != nil {
		return source.Fetched{}, err
	}
	v, ok := f.Videos[r.Name]
	if !ok {
		return source.Fetched{}, &source.Error{Kind: source.NotFound, Message: "视频不存在、已删除或不可见"}
	}
	return v, nil
}

func (f *FakeCollector) ParseCollectionLink(_ context.Context, link string) ([]source.CollectionCandidate, error) {
	candidate := func(kind string, r fakeCollectionRef) source.CollectionCandidate {
		b, _ := json.Marshal(r)
		return source.CollectionCandidate{Kind: kind, Ref: b}
	}
	for _, prefix := range []string{"list/", "alias-list/"} {
		if name, ok := strings.CutPrefix(link, prefix); ok {
			return []source.CollectionCandidate{candidate("list", fakeCollectionRef{List: name})}, nil
		}
	}
	if name, ok := strings.CutPrefix(link, "both/"); ok {
		return []source.CollectionCandidate{candidate("pages", fakeCollectionRef{Pages: name}), candidate("list", fakeCollectionRef{List: name})}, nil
	}
	if strings.HasPrefix(link, "series/") {
		return nil, &source.Error{Kind: source.InvalidLink, Message: "暂不支持系列"}
	}
	return nil, source.ErrUnrecognized
}

func (f *FakeCollector) ListCollection(ctx context.Context, ref source.CollectionRef) (source.Collection, error) {
	var r fakeCollectionRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Collection{}, err
	}
	f.mu.Lock()
	f.lists++
	f.mu.Unlock()
	if f.ListStarted != nil {
		f.ListStarted <- struct{}{}
		select {
		case <-f.ListRelease:
		case <-ctx.Done():
			return source.Collection{}, UpstreamErr(ctx)
		}
	}
	if f.ListErr != nil {
		return source.Collection{}, f.ListErr
	}
	c, ok := f.Collections[r.List+r.Pages]
	if !ok {
		return source.Collection{}, &source.Error{Kind: source.NotFound, Message: "合集不存在或已删除"}
	}
	return c, nil
}

func (f *FakeCollector) DescribeCollection(ref source.CollectionRef) (source.Display, error) {
	var r fakeCollectionRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Display{}, err
	}
	if r.Pages != "" {
		return source.Display{URL: "https://fake.test/pages/" + r.Pages, Label: "假多 P " + r.Pages}, nil
	}
	return source.Display{URL: "https://fake.test/list/" + r.List, Label: "假合集 " + r.List}, nil
}

// Gate 让之后的 Fetch 停住：在 Started 上报自己的名字，等 Release 放行一次再继续。
func (f *FakeCollector) Gate() {
	f.Started = make(chan string)
	f.Release = make(chan struct{})
}

// ListCount ListCollection 被调用的次数。
func (f *FakeCollector) ListCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists
}

// FetchedNames Fetch 过的弹幕源名字，按调用顺序。
func (f *FakeCollector) FetchedNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.fetched)
}
