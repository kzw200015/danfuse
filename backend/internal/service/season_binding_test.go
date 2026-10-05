package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// fakeCollector 实现 source.Adapter 与 source.Collector 的假适配器，不联网，ID 为 fake。
//   - 合集链接 "list/<名字>" 与 "alias-list/<名字>" 是同一个合集的两种写法，识别为一个种类为 list 的候选 {"list":"<名字>"}；
//     "both/<名字>" 识别为两个候选：种类 pages 的 {"pages":"<名字>"} 在前，种类 list 的 {"list":"<名字>"} 在后；
//     "series/<名字>" 为 InvalidLink"暂不支持系列"；其他链接认不出。
//   - ListCollection 按名字返回 collections 里的合集（两种候选都按名字找），没有时为 NotFound；listErr 不为 nil 时一律返回它。
//     listGate 后，ListCollection 先在 listStarted 上报到，再等 listRelease 关闭。
//   - 条目的弹幕源 ref 为 {"name":"<名字>"}，Fetch 返回 videos 里这个名字的结果，没有时为 NotFound；fetchErrs 里有这个名字时返回那个错误。
//     gate 后，Fetch 先在 started 上报自己的名字，再从 release 收到一次放行才继续；ctx 结束时与真实的适配器一样按 Upstream 失败。
//
// 测试在后台补建停下来（synctest.Wait）之后才改它的字段；后台读写的计数和记录有锁保护。
type fakeCollector struct {
	collections map[string]source.Collection
	listErr     error
	videos      map[string]source.Fetched
	fetchErrs   map[string]error

	started     chan string
	release     chan struct{}
	listStarted chan struct{}
	listRelease chan struct{}

	mu      sync.Mutex
	lists   int      // ListCollection 被调用的次数
	fetched []string // Fetch 过的弹幕源名字，按调用顺序
}

type fakeCollectionRef struct {
	List  string `json:"list,omitempty"`
	Pages string `json:"pages,omitempty"`
}

func (f *fakeCollector) ID() string                 { return "fake" }
func (f *fakeCollector) Platform() danmaku.Platform { return danmaku.PlatformNone }

func (f *fakeCollector) Describe(ref source.Ref) (source.Display, error) {
	var r fakeRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Display{}, err
	}
	return source.Display{URL: "https://fake.test/" + r.Name, Label: "假弹幕源 " + r.Name}, nil
}

func (f *fakeCollector) Fetch(ctx context.Context, ref source.Ref) (source.Fetched, error) {
	var r fakeRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Fetched{}, err
	}
	f.mu.Lock()
	f.fetched = append(f.fetched, r.Name)
	f.mu.Unlock()
	if f.started != nil {
		upstream := &source.Error{Kind: source.Upstream, Message: "B 站接口异常", Err: ctx.Err()}
		select {
		case f.started <- r.Name:
		case <-ctx.Done():
			return source.Fetched{}, upstream
		}
		select {
		case <-f.release:
		case <-ctx.Done():
			return source.Fetched{}, upstream
		}
	}
	if err := f.fetchErrs[r.Name]; err != nil {
		return source.Fetched{}, err
	}
	v, ok := f.videos[r.Name]
	if !ok {
		return source.Fetched{}, &source.Error{Kind: source.NotFound, Message: "视频不存在、已删除或不可见"}
	}
	return v, nil
}

func (f *fakeCollector) ParseCollectionLink(_ context.Context, link string) ([]source.Candidate, error) {
	candidate := func(kind string, r fakeCollectionRef) source.Candidate {
		b, _ := json.Marshal(r)
		return source.Candidate{Kind: kind, Ref: b}
	}
	for _, prefix := range []string{"list/", "alias-list/"} {
		if name, ok := strings.CutPrefix(link, prefix); ok {
			return []source.Candidate{candidate("list", fakeCollectionRef{List: name})}, nil
		}
	}
	if name, ok := strings.CutPrefix(link, "both/"); ok {
		return []source.Candidate{candidate("pages", fakeCollectionRef{Pages: name}), candidate("list", fakeCollectionRef{List: name})}, nil
	}
	if strings.HasPrefix(link, "series/") {
		return nil, &source.Error{Kind: source.InvalidLink, Message: "暂不支持系列"}
	}
	return nil, source.ErrUnrecognized
}

func (f *fakeCollector) ListCollection(ctx context.Context, ref source.CollectionRef) (source.Collection, error) {
	var r fakeCollectionRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Collection{}, err
	}
	f.mu.Lock()
	f.lists++
	f.mu.Unlock()
	if f.listStarted != nil {
		f.listStarted <- struct{}{}
		select {
		case <-f.listRelease:
		case <-ctx.Done():
			return source.Collection{}, &source.Error{Kind: source.Upstream, Message: "B 站接口异常", Err: ctx.Err()}
		}
	}
	if f.listErr != nil {
		return source.Collection{}, f.listErr
	}
	c, ok := f.collections[r.List+r.Pages]
	if !ok {
		return source.Collection{}, &source.Error{Kind: source.NotFound, Message: "合集不存在或已删除"}
	}
	return c, nil
}

func (f *fakeCollector) DescribeCollection(ref source.CollectionRef) (source.Display, error) {
	var r fakeCollectionRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Display{}, err
	}
	if r.Pages != "" {
		return source.Display{URL: "https://fake.test/pages/" + r.Pages, Label: "假多 P " + r.Pages}, nil
	}
	return source.Display{URL: "https://fake.test/list/" + r.List, Label: "假合集 " + r.List}, nil
}

// gate 让之后的 Fetch 停住：在 started 上报自己的名字，等 release 放行一次再继续。
func (f *fakeCollector) gate() {
	f.started = make(chan string)
	f.release = make(chan struct{})
}

func (f *fakeCollector) listCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lists
}

// fetchedNames Fetch 过的弹幕源名字，按调用顺序。
func (f *fakeCollector) fetchedNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.fetched)
}

// entry 合集里序号为 n 的条目，弹幕源名字为 name。
func entry(name string, n int) source.CollectionItem {
	return source.CollectionItem{Ref: source.Ref(`{"name":"` + name + `"}`), Number: n, Label: name}
}

// odd 对不上的条目。
func odd(name, reason string) source.CollectionItem {
	return source.CollectionItem{Ref: source.Ref(`{"name":"` + name + `"}`), Unmatched: reason, Label: name}
}

// fakeVideos 每个名字一个弹幕源，各两条弹幕。
func fakeVideos(names ...string) map[string]source.Fetched {
	videos := make(map[string]source.Fetched, len(names))
	for _, name := range names {
		videos[name] = source.Fetched{Title: "弹幕源 " + name, Duration: 1420, Danmaku: []danmaku.Danmaku{
			{SourceID: 1, TimeMs: 0, Mode: danmaku.ModeScroll, Text: name + " 前排"},
			{SourceID: 2, TimeMs: 1500, Mode: danmaku.ModeTop, Color: 0xFFFFFF, Text: name + " 来了"},
		}}
	}
	return videos
}

// seasonEnv 季绑定测试的环境：一部剧的第 1 季（季 ID 1）有给定集号的集，第 2 季（季 ID 2）没有集；
// 源适配器只注册了 src；SeasonBindingService 在后台运行。
type seasonEnv struct {
	t    *testing.T
	pool *pgxpool.Pool
	src  *fakeCollector
	svc  *SeasonBindingService
	stop func()       // 取消 Run 并等它返回
	logs lockedBuffer // 服务的日志，同时写进测试输出
}

// newSeasonEnv 在 synctest 气泡里调用，pool 是气泡里新建的连接池。集的建出时间为现在（假时间），与追更比较的时间一致。
func newSeasonEnv(t *testing.T, pool *pgxpool.Pool, src *fakeCollector, episodes ...int) *seasonEnv {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO series (type, title) VALUES ('tv', '星海旅人');
		INSERT INTO seasons (series_id, number) VALUES (1, 1), (1, 2);`)
	if err != nil {
		t.Fatal(err)
	}
	e := &seasonEnv{t: t, pool: pool, src: src}
	for _, n := range episodes {
		e.addEpisode(n)
	}
	e.start()
	return e
}

// start 构造新的 SeasonBindingService 并在后台运行，像重启了服务。
func (e *seasonEnv) start() {
	store := repository.NewStore(e.pool)
	sources := source.NewRegistry(e.src)
	logger := slogTo(io.MultiWriter(e.t.Output(), &e.logs))
	e.svc = NewSeasonBindingService(store, e.pool, sources, NewBindingService(store, sources, logger), logger)
	e.stop = runInBackground(e.t, e.svc)
}

// addEpisode 在第 1 季里新建一集，建出时间为现在（假时间），返回集 ID。
func (e *seasonEnv) addEpisode(number int) int64 {
	e.t.Helper()
	var id int64
	err := e.pool.QueryRow(e.t.Context(), `
		INSERT INTO episodes (season_id, number, duration, created_at) VALUES (1, $1, 1420, $2) RETURNING id`,
		number, time.Now()).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *seasonEnv) exec(sql string, args ...any) {
	e.t.Helper()
	if _, err := e.pool.Exec(e.t.Context(), sql, args...); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
}

// create 用链接 list/s 给第 1 季创建季绑定，等后台的补建结束，返回创建时的详情。
func (e *seasonEnv) create(from, to int) SeasonBindingDetail {
	e.t.Helper()
	d, err := e.svc.Create(e.t.Context(), 1, CreateSeasonBinding{Link: "list/s", Mapping: source.Mapping{From: from, To: to}})
	if err != nil {
		e.t.Fatalf("Create: %v", err)
	}
	synctest.Wait()
	return d
}

// backfill 立即补建一次，等它结束。
func (e *seasonEnv) backfill(id int64) {
	e.t.Helper()
	if err := e.svc.Backfill(e.t.Context(), id); err != nil {
		e.t.Fatalf("Backfill(%d): %v", id, err)
	}
	synctest.Wait()
}

func (e *seasonEnv) get(id int64) SeasonBindingDetail {
	e.t.Helper()
	d, err := e.svc.Get(e.t.Context(), id)
	if err != nil {
		e.t.Fatalf("Get(%d): %v", id, err)
	}
	return d
}

// bindings 库里的全部绑定："集号 弹幕源名字 季绑定"，手动建的季绑定写作 -，按集号、绑定 ID 排序。
func (e *seasonEnv) bindings() []string {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `
		SELECT format('%s %s %s', e.number, b.ref->>'name', coalesce(b.season_binding_id::text, '-'))
		FROM bindings b
		JOIN episodes e ON e.id = b.episode_id
		ORDER BY e.number, b.id`)
	if err != nil {
		e.t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

// states 条目表："标签 状态 集号 原因"，没有的写作 -。
func states(d SeasonBindingDetail) []string {
	got := make([]string, len(d.Items))
	for i, it := range d.Items {
		episode := "-"
		if it.EpisodeNumber != nil {
			episode = fmt.Sprint(*it.EpisodeNumber)
		}
		got[i] = strings.TrimSpace(fmt.Sprintf("%s %s %s %s", it.Label, it.State, episode, emptyIfNull(it.Reason)))
	}
	return got
}

func assertStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %q\nwant %q", what, got, want)
	}
}

func TestPreviewSeasonBinding(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{collections: map[string]source.Collection{
			// B 站第二部分接着编号，从 14 开始；本季是第 1～3 集
			"re0": {Title: "Re:0 后半", Finished: true, Items: []source.CollectionItem{
				entry("e14", 14), entry("e15", 15), odd("sp", "集号「SP」不是整数"), entry("d1", 16), entry("d2", 16),
				{Ref: source.Ref(`{"name":"e17"}`), Number: 17, Label: "e17", Note: "共 2 个分 P，只用 P1"},
			}},
		}}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)

		got, err := env.svc.Preview(t.Context(), 1, "list/re0")
		if err != nil {
			t.Fatalf("Preview: %v", err)
		}
		want := CollectionPreview{Candidates: []PreviewCandidate{{
			Kind: "list", Title: "Re:0 后半", SourceURL: "https://fake.test/list/re0", SourceLabel: "假合集 re0", Finished: true,
			DefaultMapping: MappingView{From: 14, To: 1},
			Items: []PreviewItem{
				{Label: "e14", Number: new(14)},
				{Label: "e15", Number: new(15)},
				{Label: "sp", UnmatchedReason: new("集号「SP」不是整数")},
				{Label: "d1", UnmatchedReason: new("集号重复")},
				{Label: "d2", UnmatchedReason: new("集号重复")},
				{Label: "e17", Number: new(17), Note: new("共 2 个分 P，只用 P1")},
			},
		}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Preview() = %+v\nwant %+v", got, want)
		}

		// 多个候选按适配器给的顺序
		got, err = env.svc.Preview(t.Context(), 1, "both/re0")
		if err != nil || len(got.Candidates) != 2 || got.Candidates[0].Kind != "pages" || got.Candidates[1].Kind != "list" {
			t.Errorf("Preview(both) = (%+v, %v), want pages、list 两个候选", got, err)
		}

		if n := queryInt(t, pool, `SELECT count(*) FROM season_bindings`); n != 0 {
			t.Errorf("预览写入了 %d 个季绑定", n)
		}
	})
}

func TestPreviewSeasonBindingFailed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		seasonID    int64
		link        string
		listErr     error
		wantStatus  int
		wantMessage string
		wantLists   int
	}{
		{name: "季不存在：不请求平台", seasonID: 9, link: "list/s", wantStatus: http.StatusNotFound, wantMessage: "季不存在"},
		{name: "无法识别的链接", seasonID: 1, link: "https://example.com/v/1", wantStatus: http.StatusBadRequest, wantMessage: "无法识别的链接"},
		{name: "不能作为合集绑定", seasonID: 1, link: "series/s", wantStatus: http.StatusBadRequest, wantMessage: "暂不支持系列"},
		{name: "合集不存在", seasonID: 1, link: "list/gone", wantStatus: http.StatusUnprocessableEntity, wantMessage: "合集不存在或已删除", wantLists: 1},
		{
			name: "限流", seasonID: 1, link: "list/s", wantLists: 1,
			listErr:    &source.Error{Kind: source.RateLimited, Message: "B 站限流，请稍后再试"},
			wantStatus: http.StatusBadGateway, wantMessage: "B 站限流，请稍后再试",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				src := &fakeCollector{collections: map[string]source.Collection{"s": {Title: "合集"}}, listErr: tt.listErr}
				env := newSeasonEnv(t, pool, src, 1)

				_, err := env.svc.Preview(t.Context(), tt.seasonID, tt.link)

				assertAppError(t, err, tt.wantStatus, tt.wantMessage)
				if n := src.listCount(); n != tt.wantLists {
					t.Errorf("列出了 %d 次合集，want %d", n, tt.wantLists)
				}
			})
		})
	}
}

// TestCreateSeasonBinding 创建后在后台补建：对得上、目录里有的集建出绑定，季绑定 ID 指向它；
// 对不上、在起点之前、目录里没有的集都不建。集上已有同一个弹幕源的手动绑定时记为处理过、不拉取；集上有别的绑定时照建。
func TestCreateSeasonBinding(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Title: "某番剧", Finished: true, Items: []source.CollectionItem{
				entry("z", 0), entry("a", 1), entry("b", 2), entry("c", 3), odd("sp", "集号「SP」不是整数"), entry("e", 5),
			}}},
			videos: fakeVideos("z", "a", "b", "c", "e", "other"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3, 4)
		// 集 2 已经手动绑过 b，集 3 手动绑了别的弹幕源
		env.exec(`
			INSERT INTO bindings (episode_id, adapter, ref, title, duration) VALUES
				(2, 'fake', '{"name": "b"}', '弹幕源 b', 1420),
				(3, 'fake', '{"name": "other"}', '弹幕源 other', 1420)`)
		start := time.Now()

		created := env.create(1, 1)

		if !created.Running || created.Follow != true || created.Status != "active" {
			t.Errorf("创建时的详情 = %+v, want 正在补建、追更开着、正常", created.SeasonBindingView)
		}
		assertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b -", "3 other -", "3 c 1"})
		assertStrings(t, "拉取", src.fetchedNames(), []string{"a", "c"})

		got := env.get(created.ID)
		want := SeasonBindingView{
			ID: 1, SeasonID: 1, Adapter: "fake", SourceURL: "https://fake.test/list/s", SourceLabel: "假合集 s",
			Title: "某番剧", Finished: true, MappingFrom: 1, MappingTo: 1, Follow: true, Status: "active",
			LastCheckedAt: &start, BindingCount: 2,
		}
		if got.LastCheckedAt == nil || !got.LastCheckedAt.Equal(start) {
			t.Errorf("lastCheckedAt = %v, want 这一轮的开始时间 %v", got.LastCheckedAt, start)
		}
		got.LastCheckedAt = &start
		if !reflect.DeepEqual(got.SeasonBindingView, want) {
			t.Errorf("Get() = %+v\nwant %+v", got.SeasonBindingView, want)
		}
		assertStrings(t, "条目", states(got), []string{
			"z beforeStart -", "a bound 1", "b bound 2", "c bound 3", "sp unmatched - 集号「SP」不是整数", "e waitingEpisode 5",
		})
	})
}

func TestCreateSeasonBindingFailed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		seasonID    int64
		link, kind  string
		listErr     error
		wantStatus  int
		wantMessage string
	}{
		{name: "季不存在", seasonID: 9, link: "list/s", wantStatus: http.StatusNotFound, wantMessage: "季不存在"},
		{name: "无法识别的链接", seasonID: 1, link: "bad", wantStatus: http.StatusBadRequest, wantMessage: "无法识别的链接"},
		{name: "多个候选而没给种类", seasonID: 1, link: "both/s", wantStatus: http.StatusBadRequest, wantMessage: "链接对应多个合集，请选择一个"},
		{name: "种类不在候选里", seasonID: 1, link: "list/s", kind: "pages", wantStatus: http.StatusBadRequest, wantMessage: "链接里没有这种合集，请重新预览"},
		{name: "合集不存在", seasonID: 1, link: "list/gone", wantStatus: http.StatusUnprocessableEntity, wantMessage: "合集不存在或已删除"},
		{
			name: "上游故障", seasonID: 1, link: "list/s", listErr: &source.Error{Kind: source.Upstream, Message: "B 站接口异常"},
			wantStatus: http.StatusBadGateway, wantMessage: "B 站接口异常",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				src := &fakeCollector{collections: map[string]source.Collection{"s": {Title: "合集"}}, listErr: tt.listErr}
				env := newSeasonEnv(t, pool, src, 1)

				_, err := env.svc.Create(t.Context(), tt.seasonID, CreateSeasonBinding{Link: tt.link, Kind: tt.kind, Mapping: source.Mapping{From: 1, To: 1}})

				assertAppError(t, err, tt.wantStatus, tt.wantMessage)
				if n := queryInt(t, pool, `SELECT count(*) FROM season_bindings`); n != 0 {
					t.Errorf("写入了 %d 个季绑定", n)
				}
			})
		})
	}
}

// TestCreateSeasonBindingDuplicate 同一季重复绑定同一个合集（不论链接怎么写）为 409，不再列出合集；
// 一季可以有多个季绑定，同一个合集也可以绑到别的季上。按种类选候选。
func TestCreateSeasonBindingDuplicate(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{collections: map[string]source.Collection{"s": {Title: "合集 s"}, "t": {Title: "合集 t"}}}
		env := newSeasonEnv(t, pool, src)
		env.create(1, 1)
		lists := src.listCount()

		_, err := env.svc.Create(t.Context(), 1, CreateSeasonBinding{Link: "alias-list/s", Mapping: source.Mapping{From: 1, To: 1}})
		assertAppError(t, err, http.StatusConflict, "这一季已经绑定过这个合集")
		if n := src.listCount(); n != lists {
			t.Errorf("又列出了 %d 次合集，want 0：重复的不列出", n-lists)
		}

		if _, err := env.svc.Create(t.Context(), 1, CreateSeasonBinding{Link: "both/t", Kind: "list", Mapping: source.Mapping{From: 1, To: 1}}); err != nil {
			t.Errorf("同一季绑定另一个合集：%v", err)
		}
		if _, err := env.svc.Create(t.Context(), 2, CreateSeasonBinding{Link: "list/s", Mapping: source.Mapping{From: 13, To: 1}}); err != nil {
			t.Errorf("同一个合集绑到另一季：%v", err)
		}
		synctest.Wait()
		want := []string{`1 {"list": "s"}`, `1 {"list": "t"}`, `2 {"list": "s"}`}
		rows, err := pool.Query(t.Context(), `SELECT format('%s %s', season_id, ref) FROM season_bindings ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		assertStrings(t, "季绑定", got, want)
	})
}

// TestCreateSeasonBindingSeasonDeleted 列出合集期间这一季被删除：404"这一季已被删除"，什么都不写。
func TestCreateSeasonBindingSeasonDeleted(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{collections: map[string]source.Collection{"s": {Title: "合集"}}}
		env := newSeasonEnv(t, pool, src, 1)
		src.listStarted, src.listRelease = make(chan struct{}), make(chan struct{})

		errc := make(chan error, 1)
		go func() {
			_, err := env.svc.Create(t.Context(), 1, CreateSeasonBinding{Link: "list/s", Mapping: source.Mapping{From: 1, To: 1}})
			errc <- err
		}()
		<-src.listStarted
		env.exec(`DELETE FROM seasons WHERE id = 1`)
		close(src.listRelease)

		assertAppError(t, <-errc, http.StatusNotFound, "这一季已被删除")
		if n := queryInt(t, pool, `SELECT count(*) FROM season_bindings`); n != 0 {
			t.Errorf("写入了 %d 个季绑定", n)
		}
	})
}

// TestBackfillKeepsDeletedBinding 删掉补建出的绑定后再补建：不会建回来，条目为"绑定已被删除"。
func TestBackfillKeepsDeletedBinding(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
			videos:      fakeVideos("a", "b"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		id := env.create(1, 1).ID
		env.exec(`DELETE FROM bindings WHERE ref = '{"name": "a"}'`)

		env.backfill(id)

		assertStrings(t, "绑定", env.bindings(), []string{"2 b 1"})
		assertStrings(t, "拉取", src.fetchedNames(), []string{"a", "b"})
		assertStrings(t, "条目", states(env.get(id)), []string{"a bindingDeleted 1", "b bound 2"})
	})
}

// TestBackfillRecreatedEpisode 删掉的集被同步用新 ID 建回来：算作新集，照常补建。
func TestBackfillRecreatedEpisode(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}},
			videos:      fakeVideos("a"),
		}
		env := newSeasonEnv(t, pool, src, 1)
		id := env.create(1, 1).ID
		env.exec(`DELETE FROM episodes WHERE id = 1`)
		assertStrings(t, "删掉集之后的条目", states(env.get(id)), []string{"a waitingEpisode 1"})
		newID := env.addEpisode(1)

		env.backfill(id)

		assertStrings(t, "绑定", env.bindings(), []string{"1 a 1"})
		if n := queryInt(t, pool, `SELECT count(*) FROM bindings WHERE episode_id = $1`, newID); n != 1 {
			t.Errorf("新的集上有 %d 个绑定，want 1", n)
		}
		assertStrings(t, "拉取", src.fetchedNames(), []string{"a", "a"})
	})
}

// TestBackfillNewItemsAndEpisodes 合集新增条目、目录新增集：下一轮只处理新出现的组合。
func TestBackfillNewItemsAndEpisodes(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}}},
			videos:      fakeVideos("a", "b", "c", "d"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		id := env.create(1, 1).ID
		assertStrings(t, "第一轮的拉取", src.fetchedNames(), []string{"a", "b"})

		src.collections["s"] = source.Collection{Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3), entry("d", 4)}}
		env.addEpisode(3)
		env.backfill(id)

		assertStrings(t, "拉取", src.fetchedNames(), []string{"a", "b", "c"})
		assertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b 1", "3 c 1"})
		assertStrings(t, "条目", states(env.get(id)), []string{"a bound 1", "b bound 2", "c bound 3", "d waitingEpisode 4"})

		// 合集里去掉了的条目从条目表里删掉，已经建出的绑定不动
		src.collections["s"] = source.Collection{Items: []source.CollectionItem{entry("b", 2), entry("c", 3)}}
		env.backfill(id)
		assertStrings(t, "去掉条目之后", states(env.get(id)), []string{"b bound 2", "c bound 3"})
		assertStrings(t, "去掉条目之后的绑定", env.bindings(), []string{"1 a 1", "2 b 1", "3 c 1"})
	})
}

// TestUpdateMapping 改集号对应：已处理过的不动，等待中的按新对应建出；改完立即在后台补建。
func TestUpdateMapping(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 13), entry("b", 14), entry("c", 15)}}},
			videos:      fakeVideos("a", "b", "c"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		id := env.create(13, 2).ID
		assertStrings(t, "改之前的条目", states(env.get(id)), []string{"a bound 2", "b bound 3", "c waitingEpisode 4"})

		got, err := env.svc.Update(t.Context(), id, UpdateSeasonBinding{MappingFrom: new(int32(14)), MappingTo: new(int32(1))})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got.MappingFrom != 14 || got.MappingTo != 1 || !got.Running {
			t.Errorf("Update() = %+v, want 14 = 1、正在补建", got.SeasonBindingView)
		}
		synctest.Wait()

		assertStrings(t, "绑定", env.bindings(), []string{"2 a 1", "2 c 1", "3 b 1"})
		assertStrings(t, "条目", states(env.get(id)), []string{"a bound 2", "b bound 3", "c bound 2"})

		_, err = env.svc.Update(t.Context(), 9, UpdateSeasonBinding{Follow: new(false)})
		assertAppError(t, err, http.StatusNotFound, "季绑定不存在")
	})
}

// TestBackfillItemError 单个条目上游错误：其他条目照建，失败原因记在条目上；下一轮再试，成功后清掉。
func TestBackfillItemError(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}}},
			videos:      fakeVideos("a", "b", "c"),
			fetchErrs:   map[string]error{"b": &source.Error{Kind: source.Upstream, Message: "B 站接口异常", Err: errors.New("HTTP 503")}},
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		failedAt := time.Now()
		id := env.create(1, 1).ID

		d := env.get(id)
		assertStrings(t, "条目", states(d), []string{"a bound 1", "b failed 2 B 站接口异常", "c bound 3"})
		if at := d.Items[1].LastErrorAt; at == nil || !at.Equal(failedAt) {
			t.Errorf("lastErrorAt = %v, want %v", at, failedAt)
		}
		if d.LastError != nil {
			t.Errorf("lastError = %q, want 条目的失败不算这一轮的错误", *d.LastError)
		}

		delete(src.fetchErrs, "b")
		env.backfill(id)

		d = env.get(id)
		assertStrings(t, "再补建一次之后的条目", states(d), []string{"a bound 1", "b bound 2", "c bound 3"})
		if d.Items[1].LastErrorAt != nil {
			t.Error("成功之后 lastErrorAt 应清掉")
		}
		assertStrings(t, "拉取", src.fetchedNames(), []string{"a", "b", "c", "b"})
	})
}

// TestBackfillRateLimited 限流：这一轮立即结束，后面的条目没有请求，上次检查时间照常更新、记下原因；下一轮接着做。
func TestBackfillRateLimited(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}}},
			videos:      fakeVideos("a", "b", "c"),
			fetchErrs:   map[string]error{"b": &source.Error{Kind: source.RateLimited, Message: "B 站限流，请稍后再试"}},
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		start := time.Now()
		id := env.create(1, 1).ID

		assertStrings(t, "拉取", src.fetchedNames(), []string{"a", "b"})
		d := env.get(id)
		if d.LastError == nil || *d.LastError != "B 站限流，请稍后再试" || d.LastCheckedAt == nil || !d.LastCheckedAt.Equal(start) {
			t.Errorf("lastError = %v, lastCheckedAt = %v, want 限流的原因、这一轮的开始时间", d.LastError, d.LastCheckedAt)
		}
		assertStrings(t, "条目", states(d), []string{"a bound 1", "b pending 2", "c pending 3"})

		delete(src.fetchErrs, "b")
		env.backfill(id)
		assertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b 1", "3 c 1"})
		if d := env.get(id); d.LastError != nil {
			t.Errorf("lastError = %q, want 成功后清掉", *d.LastError)
		}
	})
}

// TestBackfillCollectionGone 列出合集返回 NotFound：季绑定失效，建出的绑定不受影响；之后列出成功时恢复正常。
// 其他上游错误只记下原因，状态不变。
func TestBackfillCollectionGone(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}},
			videos:      fakeVideos("a"),
		}
		env := newSeasonEnv(t, pool, src, 1)
		id := env.create(1, 1).ID

		for _, step := range []struct {
			name       string
			listErr    error
			wantStatus string
			wantError  string
		}{
			{"合集被删除", &source.Error{Kind: source.NotFound, Message: "合集不存在或已删除"}, "dead", "合集不存在或已删除"},
			{"失效期间接口异常：仍然失效", &source.Error{Kind: source.Upstream, Message: "B 站接口异常"}, "dead", "B 站接口异常"},
			{"合集恢复", nil, "active", ""},
		} {
			src.listErr = step.listErr
			env.backfill(id)
			d := env.get(id)
			if d.Status != step.wantStatus || emptyIfNull(d.LastError) != step.wantError {
				t.Errorf("%s：状态 %s、错误 %q，want %s、%q", step.name, d.Status, emptyIfNull(d.LastError), step.wantStatus, step.wantError)
			}
			assertStrings(t, step.name+"：绑定", env.bindings(), []string{"1 a 1"})
		}
	})
}

// TestDeleteSeasonBinding 删除季绑定：默认保留它建出的绑定（变成普通绑定），withBindings 时一起删掉；条目与处理过的记录随之删除。
func TestDeleteSeasonBinding(t *testing.T) {
	t.Parallel()
	for _, withBindings := range []bool{false, true} {
		t.Run(fmt.Sprintf("withBindings=%v", withBindings), func(t *testing.T) {
			t.Parallel()
			syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				src := &fakeCollector{
					collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
					videos:      fakeVideos("a", "b", "m"),
				}
				env := newSeasonEnv(t, pool, src, 1, 2)
				env.exec(`INSERT INTO bindings (episode_id, adapter, ref, title, duration) VALUES (1, 'fake', '{"name": "m"}', '手动', 1420)`)
				id := env.create(1, 1).ID

				if err := env.svc.Delete(t.Context(), id, withBindings); err != nil {
					t.Fatalf("Delete: %v", err)
				}

				want := []string{"1 m -", "1 a -", "2 b -"}
				if withBindings {
					want = []string{"1 m -"}
				}
				assertStrings(t, "绑定", env.bindings(), want)
				for _, table := range []string{"season_bindings", "season_binding_items", "season_binding_handled"} {
					if n := queryInt(t, pool, `SELECT count(*) FROM `+table); n != 0 {
						t.Errorf("%s 还有 %d 行", table, n)
					}
				}
				assertAppError(t, env.svc.Delete(t.Context(), id, withBindings), http.StatusNotFound, "季绑定不存在")
			})
		})
	}
}

// TestDeleteSeasonCascades 删除一季时它的季绑定一起删除。
func TestDeleteSeasonCascades(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}}, videos: fakeVideos("a")}
		env := newSeasonEnv(t, pool, src, 1)
		id := env.create(1, 1).ID

		env.exec(`DELETE FROM seasons WHERE id = 1`)

		_, err := env.svc.Get(t.Context(), id)
		assertAppError(t, err, http.StatusNotFound, "季绑定不存在")
	})
}

// TestDeleteSeasonBindingDuringBackfill 补建停在拉取上时删除季绑定：补建随即结束。一起删时，之前建出的绑定也删掉、
// 正在建的不会漏下；不一起删时，之前建出的绑定变成普通绑定，正在建的不再建出。
func TestDeleteSeasonBindingDuringBackfill(t *testing.T) {
	t.Parallel()
	for _, withBindings := range []bool{false, true} {
		t.Run(fmt.Sprintf("withBindings=%v", withBindings), func(t *testing.T) {
			t.Parallel()
			syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				src := &fakeCollector{
					collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}}},
					videos:      fakeVideos("a", "b", "c"),
				}
				env := newSeasonEnv(t, pool, src, 1, 2, 3)
				src.gate()
				d, err := env.svc.Create(t.Context(), 1, CreateSeasonBinding{Link: "list/s", Mapping: source.Mapping{From: 1, To: 1}})
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				<-src.started // a
				src.release <- struct{}{}
				<-src.started // b：a 已经写入

				if err := env.svc.Delete(t.Context(), d.ID, withBindings); err != nil {
					t.Fatalf("Delete: %v", err)
				}
				src.release <- struct{}{}
				synctest.Wait()

				want := []string{"1 a -"}
				if withBindings {
					want = nil
				}
				assertStrings(t, "绑定", env.bindings(), want)
				assertStrings(t, "拉取", src.fetchedNames(), []string{"a", "b"})
			})
		})
	}
}

// holdRowLock 在另一个事务里执行 sql 锁住行，返回的函数回滚这个事务、放开锁。
func (e *seasonEnv) holdRowLock(sql string) (release func()) {
	e.t.Helper()
	tx, err := e.pool.Begin(e.t.Context())
	if err != nil {
		e.t.Fatal(err)
	}
	if _, err := tx.Exec(e.t.Context(), sql); err != nil {
		e.t.Fatalf("%s: %v", sql, err)
	}
	return func() { _ = tx.Rollback(context.Background()) }
}

// goDo 在后台执行 f，结果从返回的 channel 送回。
func goDo(f func() error) <-chan error {
	done := make(chan error, 1)
	go func() { done <- f() }()
	return done
}

// waitLockWaiters 等到这个库里有 n 个连接在等锁。阻塞在数据库 I/O 上时气泡里的假时间不前进，所以不 sleep，反复查询。
func (e *seasonEnv) waitLockWaiters(n int64) {
	e.t.Helper()
	for range 100000 {
		if queryInt(e.t, e.pool, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) >= n {
			return
		}
	}
	e.t.Fatalf("等不到 %d 个连接在等锁", n)
}

// TestBackfillWriteDuringDeleteSeason 删季锁住了第 1 集、还没删到季绑定时，补建开始写入第 1 集：
// 两边加锁的顺序不能相反（否则死锁），补建等删季做完，随后发现季绑定已被删除。
func TestBackfillWriteDuringDeleteSeason(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}}, videos: fakeVideos("a")}
		env := newSeasonEnv(t, pool, src, 1, 2)
		src.gate()
		if _, err := env.svc.Create(t.Context(), 1, CreateSeasonBinding{Link: "list/s", Mapping: source.Mapping{From: 1, To: 1}}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		<-src.started // a：还没开始写入

		// 第 2 集被另一个事务锁住，删季锁住季和第 1 集之后停在第 2 集上
		release := env.holdRowLock(`SELECT id FROM episodes WHERE number = 2 FOR UPDATE`)
		deleted := goDo(func() error { _, err := pool.Exec(t.Context(), `DELETE FROM seasons WHERE id = 1`); return err })
		env.waitLockWaiters(1)
		src.release <- struct{}{}
		env.waitLockWaiters(2) // 补建的写入事务也在等
		release()

		if err := <-deleted; err != nil {
			t.Fatalf("删季: %v", err)
		}
		synctest.Wait()
		if logs := env.logs.String(); strings.Contains(logs, "level=ERROR") {
			t.Errorf("补建出错：\n%s", logs)
		}
		assertStrings(t, "绑定", env.bindings(), nil)
	})
}

// TestDeleteSeasonBindingDuringDeleteSeason 删季停在第 2 集上手动建的绑定上时，删除季绑定：删季的级联在删绑定之前
// 已经锁住了季绑定，删除季绑定在第一句就排队，与删季加锁的顺序相同，不死锁；删季做完后季绑定已不存在。
func TestDeleteSeasonBindingDuringDeleteSeason(t *testing.T) {
	t.Parallel()
	for _, withBindings := range []bool{false, true} {
		t.Run(fmt.Sprintf("withBindings=%v", withBindings), func(t *testing.T) {
			t.Parallel()
			syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				src := &fakeCollector{collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}}, videos: fakeVideos("a")}
				env := newSeasonEnv(t, pool, src, 1, 2)
				env.exec(`INSERT INTO bindings (episode_id, adapter, ref, title, duration) VALUES (2, 'fake', '{"name": "m"}', '手动', 1420)`)
				id := env.create(1, 1).ID

				// 第 2 集上手动建的绑定被另一个事务锁住，删季（季 → 集 → 季绑定 → 绑定）停在它上面
				release := env.holdRowLock(`SELECT id FROM bindings WHERE ref->>'name' = 'm' FOR UPDATE`)
				deletedSeason := goDo(func() error { _, err := pool.Exec(t.Context(), `DELETE FROM seasons WHERE id = 1`); return err })
				env.waitLockWaiters(1)
				deletedBinding := goDo(func() error { return env.svc.Delete(t.Context(), id, withBindings) })
				env.waitLockWaiters(2)
				release()

				if err := <-deletedSeason; err != nil {
					t.Fatalf("删季: %v", err)
				}
				assertAppError(t, <-deletedBinding, http.StatusNotFound, "季绑定不存在")
				assertStrings(t, "绑定", env.bindings(), nil)
			})
		})
	}
}

// TestBackfillTwice 同一个季绑定正在补建时再触发：409"正在补建"，不排队；详情的 running 为 true。
func TestBackfillTwice(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}}, videos: fakeVideos("a")}
		env := newSeasonEnv(t, pool, src, 1)
		src.gate()
		d, err := env.svc.Create(t.Context(), 1, CreateSeasonBinding{Link: "list/s", Mapping: source.Mapping{From: 1, To: 1}})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		<-src.started

		assertAppError(t, env.svc.Backfill(t.Context(), d.ID), http.StatusConflict, "正在补建")
		if got := env.get(d.ID); !got.Running {
			t.Error("running = false, want 正在补建")
		}
		// 改集号对应时正在补建：不另起一轮，也不报错
		if _, err := env.svc.Update(t.Context(), d.ID, UpdateSeasonBinding{MappingTo: new(int32(1))}); err != nil {
			t.Errorf("Update: %v", err)
		}

		src.release <- struct{}{}
		synctest.Wait()
		if got := env.get(d.ID); got.Running {
			t.Error("补建结束后 running = true")
		}
		assertStrings(t, "拉取", src.fetchedNames(), []string{"a"})
		assertAppError(t, env.svc.Backfill(t.Context(), 9), http.StatusNotFound, "季绑定不存在")
	})
}

// TestBackfillEpisodeDeletedDuringFetch 补建停在拉取上时删除目标集：这个条目被跳过，补建继续。
func TestBackfillEpisodeDeletedDuringFetch(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
			videos:      fakeVideos("a", "b"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		src.gate()
		d, err := env.svc.Create(t.Context(), 1, CreateSeasonBinding{Link: "list/s", Mapping: source.Mapping{From: 1, To: 1}})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		<-src.started // a
		env.exec(`DELETE FROM episodes WHERE number = 1`)
		src.release <- struct{}{}
		<-src.started // b
		src.release <- struct{}{}
		synctest.Wait()

		assertStrings(t, "绑定", env.bindings(), []string{"2 b 1"})
		assertStrings(t, "条目", states(env.get(d.ID)), []string{"a waitingEpisode 1", "b bound 2"})
	})
}

// TestFollowChecksDaily 追更开着时按上次检查时间每满 24 小时检查一次（每分钟扫描）；关掉后不再检查。
func TestFollowChecksDaily(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{collections: map[string]source.Collection{"s": {}}}
		env := newSeasonEnv(t, pool, src)
		id := env.create(1, 1).ID
		checks := func() int { return src.listCount() - 2 } // 创建时列出一次，随后补建又检查一次

		time.Sleep(followCheckInterval - followScanInterval)
		synctest.Wait()
		if n := checks(); n != 0 {
			t.Fatalf("不到 24 小时又检查了 %d 次", n)
		}
		time.Sleep(2 * followScanInterval)
		synctest.Wait()
		if n := checks(); n != 1 {
			t.Fatalf("满 24 小时后检查了 %d 次，want 1", n)
		}

		if _, err := env.svc.Update(t.Context(), id, UpdateSeasonBinding{Follow: new(false)}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		time.Sleep(3 * followCheckInterval)
		synctest.Wait()
		if n := checks(); n != 1 {
			t.Errorf("关掉追更后又检查了 %d 次", n-1)
		}

		// 打开追更时立即补建一次
		if _, err := env.svc.Update(t.Context(), id, UpdateSeasonBinding{Follow: new(true)}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		synctest.Wait()
		if n := checks(); n != 2 {
			t.Errorf("打开追更后共检查了 %d 次，want 2", n)
		}
	})
}

// TestFollowNewEpisode 目录同步进来新的集后一分钟内，追更开着的季绑定会去合集里找这一集；追更关着时不找。
func TestFollowNewEpisode(t *testing.T) {
	t.Parallel()
	for _, follow := range []bool{true, false} {
		t.Run(fmt.Sprintf("follow=%v", follow), func(t *testing.T) {
			t.Parallel()
			syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				src := &fakeCollector{
					collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
					videos:      fakeVideos("a", "b"),
				}
				env := newSeasonEnv(t, pool, src, 1)
				id := env.create(1, 1).ID
				if !follow {
					if _, err := env.svc.Update(t.Context(), id, UpdateSeasonBinding{Follow: new(false)}); err != nil {
						t.Fatalf("Update: %v", err)
					}
				}
				time.Sleep(time.Hour)
				synctest.Wait()

				env.addEpisode(2)
				time.Sleep(followScanInterval)
				synctest.Wait()

				want := []string{"1 a 1"}
				if follow {
					want = append(want, "2 b 1")
				}
				assertStrings(t, "绑定", env.bindings(), want)
			})
		})
	}
}

// TestFollowRechecksDue 扫描列出到期的季绑定之后，排在后面的被手动补建检查过：轮到它时已不再到期，不重复检查。
func TestFollowRechecksDue(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{collections: map[string]source.Collection{"s": {}, "t": {}}, videos: fakeVideos("a")}
		env := newSeasonEnv(t, pool, src, 1)
		env.create(1, 1)
		second, err := env.svc.Create(t.Context(), 1, CreateSeasonBinding{Link: "list/t", Mapping: source.Mapping{From: 1, To: 1}})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		synctest.Wait()
		// 合集 s 新出了一集：每天的检查里，第一个季绑定停在拉取上
		src.collections["s"] = source.Collection{Items: []source.CollectionItem{entry("a", 1)}}
		src.gate()

		time.Sleep(followCheckInterval)
		<-src.started // 扫描列出了两个季绑定，第一个停在拉取 a 上
		lists := src.listCount()
		env.backfill(second.ID)
		src.release <- struct{}{}
		synctest.Wait()

		if n := src.listCount() - lists; n != 1 {
			t.Errorf("第二个季绑定检查了 %d 次，want 只有手动补建的 1 次", n)
		}
		assertStrings(t, "绑定", env.bindings(), []string{"1 a 1"})
	})
}

// TestFollowResumesAfterRestart 补建到一半时关闭服务：不写上次检查时间，重启后一分钟内从没做完的地方接着做。
func TestFollowResumesAfterRestart(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}}},
			videos:      fakeVideos("a", "b", "c"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		src.gate()
		d, err := env.svc.Create(t.Context(), 1, CreateSeasonBinding{Link: "list/s", Mapping: source.Mapping{From: 1, To: 1}})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		<-src.started // a
		src.release <- struct{}{}
		<-src.started // b

		env.stop() // 关闭服务：b 的拉取被取消
		if got := env.get(d.ID); got.LastCheckedAt != nil || got.Running {
			t.Errorf("关闭后 lastCheckedAt = %v、running = %v，want 不写、不在补建", got.LastCheckedAt, got.Running)
		}
		assertStrings(t, "关闭时的绑定", env.bindings(), []string{"1 a 1"})

		src.started = nil
		env.start()
		time.Sleep(followScanInterval)
		synctest.Wait()

		assertStrings(t, "重启后的绑定", env.bindings(), []string{"1 a 1", "2 b 1", "3 c 1"})
		assertStrings(t, "拉取", src.fetchedNames(), []string{"a", "b", "b", "c"})
	})
}

// fetchedAt 库里绑定 ID 为 id 的上次拉取时间。
func (e *seasonEnv) fetchedAt(id int64) time.Time {
	e.t.Helper()
	b := getBinding(e.t, e.pool, id)
	return *b.LastFetchedAt
}

// TestAutoRefetch 追更开着时，每天的检查重新拉取季绑定建出的、建出不到 14 天、距上次拉取满 24 小时的绑定，每个绑定一天最多一次；
// 手动建的绑定不拉。
func TestAutoRefetch(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
			videos:      fakeVideos("a", "b", "m"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		env.exec(`INSERT INTO bindings (episode_id, adapter, ref, title, duration, last_fetched_at) VALUES (1, 'fake', '{"name": "m"}', '手动', 1420, $1)`,
			time.Now().Add(-48*time.Hour))
		id := env.create(1, 1).ID
		start := time.Now()

		// 不到 24 小时：手动补建也不重新拉取
		time.Sleep(time.Hour)
		env.backfill(id)
		assertStrings(t, "一小时后的拉取", src.fetchedNames(), []string{"a", "b"})

		time.Sleep(followCheckInterval - time.Hour)
		synctest.Wait()
		assertStrings(t, "一天后的拉取", src.fetchedNames(), []string{"a", "b", "a", "b"})
		if at := env.fetchedAt(2); !at.Equal(start.Add(followCheckInterval)) {
			t.Errorf("上次拉取时间 = %v, want %v", at, start.Add(followCheckInterval))
		}

		// 同一天里再触发补建，不再重新拉取
		env.backfill(id)
		assertStrings(t, "同一天再补建的拉取", src.fetchedNames(), []string{"a", "b", "a", "b"})
	})
}

// TestAutoRefetchWindow 只重新拉取建出不到 14 天、距上次拉取满 24 小时的；追更关着时不拉；弹幕源不存在时标为失效。
func TestAutoRefetchWindow(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3), entry("d", 4)}}},
			videos:      fakeVideos("a", "b", "c", "d"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3, 4)
		id := env.create(1, 1).ID
		now := time.Now()
		// 绑定 1～4 依次为 a～d：a 建出满 14 天；b 建出 13 天、昨天拉取过；c 建出 13 天、23 小时前拉取过；d 同 b，但弹幕源已不存在
		env.exec(`UPDATE bindings SET created_at = $1, last_fetched_at = $2 WHERE id = 1`, now.Add(-followRefetchWindow), now.Add(-25*time.Hour))
		env.exec(`UPDATE bindings SET created_at = $1, last_fetched_at = $2 WHERE id IN (2, 4)`, now.Add(-13*24*time.Hour), now.Add(-25*time.Hour))
		env.exec(`UPDATE bindings SET created_at = $1, last_fetched_at = $2 WHERE id = 3`, now.Add(-13*24*time.Hour), now.Add(-23*time.Hour))
		delete(src.videos, "d")

		if _, err := env.svc.Update(t.Context(), id, UpdateSeasonBinding{Follow: new(false)}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		env.backfill(id)
		assertStrings(t, "追更关着时的拉取", src.fetchedNames(), []string{"a", "b", "c", "d"})

		if _, err := env.svc.Update(t.Context(), id, UpdateSeasonBinding{Follow: new(true)}); err != nil {
			t.Fatalf("Update: %v", err)
		}
		synctest.Wait()
		assertStrings(t, "拉取", src.fetchedNames(), []string{"a", "b", "c", "d", "b", "d"})
		if b := getBinding(t, pool, 4); b.Status != "dead" {
			t.Errorf("d 的状态 = %s, want dead", b.Status)
		}
	})
}

// TestAutoRefetchDueAfterCheck 绑定在每天的检查开始之后才满 24 小时（例如上次是在那一轮中途拉取的）：
// 一分钟内另起一轮重新拉取它，不等到第二天。
func TestAutoRefetchDueAfterCheck(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}},
			videos:      fakeVideos("a"),
		}
		env := newSeasonEnv(t, pool, src, 1)
		env.create(1, 1)
		// 上次拉取在这一轮开始 30 秒之后
		env.exec(`UPDATE bindings SET last_fetched_at = $1 WHERE id = 1`, time.Now().Add(30*time.Second))

		checks := func() int { return src.listCount() - 2 } // 创建时列出一次，随后补建又检查一次

		time.Sleep(followCheckInterval)
		synctest.Wait()
		assertStrings(t, "满 24 小时的检查", src.fetchedNames(), []string{"a"})
		if n := checks(); n != 1 {
			t.Fatalf("检查了 %d 次，want 1", n)
		}

		time.Sleep(followScanInterval)
		synctest.Wait()
		assertStrings(t, "一分钟后", src.fetchedNames(), []string{"a", "a"})

		// 之后不再每分钟检查
		time.Sleep(time.Hour)
		synctest.Wait()
		if n := checks(); n != 2 {
			t.Errorf("检查了 %d 次，want 2", n)
		}
	})
}

// TestManualBackfillQueue 同一时间只进行一轮手动补建：另一个季绑定的手动触发排队，不显示正在补建，
// 前一轮结束后接着开始；排着队时再触发不重复排。
func TestManualBackfillQueue(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			collections: map[string]source.Collection{
				"s": {Items: []source.CollectionItem{entry("a", 1)}},
				"t": {Items: []source.CollectionItem{entry("b", 2)}},
			},
			videos: fakeVideos("a", "b"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		src.gate()
		first, err := env.svc.Create(t.Context(), 1, CreateSeasonBinding{Link: "list/s", Mapping: source.Mapping{From: 1, To: 1}})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if name := <-src.started; name != "a" {
			t.Fatalf("第一个季绑定拉取的是 %s", name)
		}

		second, err := env.svc.Create(t.Context(), 1, CreateSeasonBinding{Link: "list/t", Mapping: source.Mapping{From: 1, To: 1}})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if second.Running {
			t.Error("排队的季绑定 running = true, want 排着队、还没开始")
		}
		if err := env.svc.Backfill(t.Context(), second.ID); err != nil {
			t.Errorf("排着队时再触发：%v", err)
		}
		if !env.get(first.ID).Running {
			t.Error("第一个季绑定 running = false")
		}

		src.release <- struct{}{}
		if name := <-src.started; name != "b" {
			t.Fatalf("第一轮结束后拉取的是 %s, want 排队的 b", name)
		}
		src.release <- struct{}{}
		synctest.Wait()

		assertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b 2"})
		assertStrings(t, "拉取", src.fetchedNames(), []string{"a", "b"})
	})
}
