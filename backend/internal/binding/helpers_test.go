package binding_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/binding/dao"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/infra/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// getBinding 读出库里的绑定。
func getBinding(t *testing.T, pool *pgxpool.Pool, id int64) dao.Binding {
	t.Helper()
	b, err := dao.New(pool).GetBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("GetBinding(%d): %v", id, err)
	}
	return b
}

// fakeAdapter 只有弹幕源的假适配器，不联网；合集的链接都不认识。
// 链接 "fake/<名字>" 与 "alias/<名字>" 是同一个弹幕源的两种写法（像 B 站的 BV 号与 av 号），ref 都是 {"name":"<名字>"}。
// Fetch 返回 sources 里这个名字的结果，没有这个名字时返回 NotFound；err 不为 nil 时一律返回 err。
// started 不为 nil 时，Fetch 开始时先在 started 上报到，再等 release 关闭，用来在拉取期间插入别的操作。
// hang 为 true 时，Fetch 一直等到 ctx 结束，像上游一直不响应。
// parseDelay 让 ParseLink 先耗时这么久、parseHang 让它一直等到 ctx 结束，像跟随短链时上游响应慢、一直不响应。
type fakeAdapter struct {
	sources    map[string]source.Fetched
	err        error
	started    chan struct{}
	release    chan struct{}
	hang       bool
	parseDelay time.Duration
	parseHang  bool
	fetches    atomic.Int32 // Fetch 被调用的次数
}

var _ source.Adapter = (*fakeAdapter)(nil)

// errNoDeadline 一直等待的 Fetch、ParseLink 收到的 ctx 没有截止时间：总时限没有生效。立即返回，免得测试一直挂着。
var errNoDeadline = errors.New("ctx 没有截止时间")

func (a *fakeAdapter) ID() string { return "fake" }

func (a *fakeAdapter) Platform() danmaku.Platform { return danmaku.PlatformNone }

func (a *fakeAdapter) Describe(ref source.Ref) (source.Display, error) {
	return testenv.DescribeFake(ref)
}

func (a *fakeAdapter) ParseLink(ctx context.Context, link string) (source.Ref, error) {
	if a.parseHang || a.parseDelay > 0 {
		if _, ok := ctx.Deadline(); !ok {
			return nil, errNoDeadline
		}
		var delay <-chan time.Time // parseHang 时为 nil，一直等到 ctx 结束
		if !a.parseHang {
			delay = time.After(a.parseDelay)
		}
		select {
		case <-delay:
		case <-ctx.Done():
			return nil, testenv.UpstreamErr(ctx)
		}
	}
	for _, prefix := range []string{"fake/", "alias/"} {
		if name, ok := strings.CutPrefix(link, prefix); ok {
			return json.Marshal(testenv.FakeRef{Name: name})
		}
	}
	return nil, source.ErrUnrecognized
}

func (a *fakeAdapter) Fetch(ctx context.Context, ref source.Ref) (source.Fetched, error) {
	a.fetches.Add(1)
	if a.hang {
		if _, ok := ctx.Deadline(); !ok {
			return source.Fetched{}, errNoDeadline
		}
		<-ctx.Done()
		return source.Fetched{}, testenv.UpstreamErr(ctx)
	}
	if a.started != nil {
		a.started <- struct{}{}
		select {
		case <-a.release:
		case <-ctx.Done():
			return source.Fetched{}, testenv.UpstreamErr(ctx)
		}
	}
	if a.err != nil {
		return source.Fetched{}, a.err
	}
	var r testenv.FakeRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Fetched{}, err
	}
	f, ok := a.sources[r.Name]
	if !ok {
		return source.Fetched{}, &source.Error{Kind: source.NotFound, Message: "视频不存在、已删除或不可见"}
	}
	return f, nil
}

func (a *fakeAdapter) ParseCollectionLink(context.Context, string) ([]source.CollectionCandidate, error) {
	return nil, source.ErrUnrecognized
}

func (a *fakeAdapter) ListCollection(context.Context, source.CollectionRef) (source.Collection, error) {
	return source.Collection{}, errors.New("fakeAdapter 没有合集")
}

func (a *fakeAdapter) DescribeCollection(source.CollectionRef) (source.Display, error) {
	return source.Display{}, errors.New("fakeAdapter 没有合集")
}

// gate 让之后的 Fetch 停在 channel 上：在 started 上报到，等 release 关闭再继续。
func (a *fakeAdapter) gate() {
	a.started = make(chan struct{})
	a.release = make(chan struct{})
}

// video 一个有弹幕的弹幕源：第 2 条与第 1 条原始 ID 相同（源内重复），写入时只留一条。
var video = source.Fetched{
	Title:    "星海旅人 / 第 1 话",
	Duration: 1418,
	Danmaku: []danmaku.Danmaku{
		{SourceID: 30, TimeMs: 1500, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "前排"},
		{SourceID: 30, TimeMs: 1500, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "前排"},
		{SourceID: 10, TimeMs: 0, Mode: danmaku.ModeTop, Color: 0, Text: "😀 第一行\n第二行"},
		{SourceID: 20, TimeMs: 61000, Mode: danmaku.ModeReverse, Color: 0xE70012, Text: `&<>"'`},
	},
	LogAttrs: []slog.Attr{slog.Int("protobuf", 4)},
}

// newBindingService 新库里写入两集（testenv.SeedEpisodes），构造只注册了 adapter 的 binding.Service。
// 测试结束时检查不变量（testenv.AssertInvariants）。
func newBindingService(t *testing.T, adapter *fakeAdapter, logger *slog.Logger) (*binding.Service, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.Pool(t)
	testenv.SeedEpisodes(t, pool)
	t.Cleanup(func() { testenv.AssertInvariants(t, pool) }) // 在关闭连接池之前
	return binding.NewService(pool, source.NewRegistry(adapter), logger), pool
}

// readDanmaku 一个绑定落库的弹幕，按原始 ID 排序。
func readDanmaku(t *testing.T, pool *pgxpool.Pool, bindingID int64) []danmaku.Danmaku {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT source_id, time_ms, mode, color, text FROM danmaku WHERE binding_id = $1 ORDER BY source_id`, bindingID)
	if err != nil {
		t.Fatal(err)
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (danmaku.Danmaku, error) {
		var d danmaku.Danmaku
		err := row.Scan(&d.SourceID, &d.TimeMs, &d.Mode, &d.Color, &d.Text)
		return d, err
	})
	if err != nil {
		t.Fatal(err)
	}
	return items
}

// assertNothingWritten 库里没有任何绑定和弹幕。
func assertNothingWritten(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	bindings := testenv.QueryInt(t, pool, `SELECT count(*) FROM bindings`)
	rows := testenv.QueryInt(t, pool, `SELECT count(*) FROM danmaku`)
	if bindings != 0 || rows != 0 {
		t.Errorf("库里有 %d 个绑定、%d 条弹幕，want 都没有", bindings, rows)
	}
}

// createS1 给集 1 绑定弹幕源 s1，返回绑定 ID。适配器这时返回 video 时，
// 绑定有原始 ID 为 10、20、30 的三条弹幕，content_version 为 1。
func createS1(t *testing.T, svc *binding.Service) int64 {
	t.Helper()
	b, err := svc.Create(t.Context(), 1, "fake/s1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return b.ID
}
