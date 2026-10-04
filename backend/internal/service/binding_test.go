package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// fakeAdapter 实现 source.Adapter 与 source.Linker 的假适配器，不联网。
// 链接 "fake/<名字>" 与 "alias/<名字>" 是同一个弹幕源的两种写法（像 B 站的 BV 号与 av 号），ref 都是 {"name":"<名字>"}。
// Fetch 返回 sources 里这个名字的结果，没有这个名字时返回 NotFound；err 不为 nil 时一律返回 err。
// started 不为 nil 时，Fetch 开始时先在 started 上报到，再等 release 关闭，用来在拉取期间插入别的操作。
// hang 为 true 时，Fetch 一直等到 ctx 结束，像上游一直不响应。
type fakeAdapter struct {
	sources map[string]source.Fetched
	err     error
	started chan struct{}
	release chan struct{}
	hang    bool
	fetches atomic.Int32 // Fetch 被调用的次数
}

// errNoDeadline hang 的 Fetch 收到的 ctx 没有截止时间：拉取的总时限没有生效。立即返回，免得测试一直挂着。
var errNoDeadline = errors.New("Fetch 的 ctx 没有截止时间")

type fakeRef struct {
	Name string `json:"name"`
}

func (a *fakeAdapter) ID() string                 { return "fake" }
func (a *fakeAdapter) Platform() danmaku.Platform { return danmaku.PlatformNone }

func (a *fakeAdapter) Describe(ref source.Ref) (source.Display, error) {
	var r fakeRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Display{}, err
	}
	return source.Display{URL: "https://fake.test/" + r.Name, Label: "假来源 " + r.Name}, nil
}

func (a *fakeAdapter) ParseLink(_ context.Context, link string) (source.Ref, error) {
	for _, prefix := range []string{"fake/", "alias/"} {
		if name, ok := strings.CutPrefix(link, prefix); ok {
			return json.Marshal(fakeRef{Name: name})
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
		return source.Fetched{}, &source.Error{Kind: source.Upstream, Message: "B 站接口异常", Err: ctx.Err()}
	}
	if a.started != nil {
		a.started <- struct{}{}
		select {
		case <-a.release:
		case <-ctx.Done(): // 与真实的适配器一样：ctx 结束时按 Upstream 失败
			return source.Fetched{}, &source.Error{Kind: source.Upstream, Message: "B 站接口异常", Err: ctx.Err()}
		}
	}
	if a.err != nil {
		return source.Fetched{}, a.err
	}
	var r fakeRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Fetched{}, err
	}
	f, ok := a.sources[r.Name]
	if !ok {
		return source.Fetched{}, &source.Error{Kind: source.NotFound, Message: "视频不存在、已删除或不可见"}
	}
	return f, nil
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

// newBindingService 新库里写入两集（seedEpisodes），构造只注册了 adapter 的 BindingService。
// 测试结束时检查不变量：每个绑定的 danmaku_count 等于它实际的弹幕条数。
func newBindingService(t *testing.T, adapter *fakeAdapter, logger *slog.Logger) (*BindingService, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.Pool(t)
	seedEpisodes(t, pool)
	t.Cleanup(func() { assertDanmakuCounts(t, pool) }) // 在关闭连接池之前
	return NewBindingService(repository.NewStore(pool), source.NewRegistry(adapter), logger), pool
}

// seedEpisodes 写入一部剧、一季、两集，集 ID 为 1、2。
func seedEpisodes(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO series (type, title) VALUES ('tv', '星海旅人');
		INSERT INTO seasons (series_id, number) VALUES (1, 1);
		INSERT INTO episodes (season_id, number, duration) VALUES (1, 1, 1420), (1, 2, 1440);`)
	if err != nil {
		t.Fatal(err)
	}
}

// waitFetching 等一次 Create 或 Refetch 进入拉取（在 started 上报到）；它在拉取之前就返回时立即失败，不挂住测试。
func waitFetching(t *testing.T, adapter *fakeAdapter, errc <-chan error) {
	t.Helper()
	select {
	case <-adapter.started:
	case err := <-errc:
		t.Fatalf("拉取之前就返回了：%v", err)
	}
}

// assertDanmakuCounts 检查不变量：每个绑定的 danmaku_count 等于它实际的弹幕条数。
func assertDanmakuCounts(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	// 在 t.Cleanup 里调用时 t.Context() 已经取消
	rows, err := pool.Query(context.Background(), `
		SELECT b.id, b.danmaku_count, count(d.source_id)
		FROM bindings b
		LEFT JOIN danmaku d ON d.binding_id = b.id
		GROUP BY b.id
		HAVING b.danmaku_count <> count(d.source_id)`)
	if err != nil {
		t.Fatal(err)
	}
	mismatches, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) ([3]int64, error) {
		var m [3]int64
		err := row.Scan(&m[0], &m[1], &m[2])
		return m, err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mismatches {
		t.Errorf("绑定 %d 的 danmaku_count 为 %d，实际有 %d 条弹幕", m[0], m[1], m[2])
	}
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

func queryInt(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

// assertNothingWritten 库里没有任何绑定和弹幕。
func assertNothingWritten(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	bindings := queryInt(t, pool, `SELECT count(*) FROM bindings`)
	rows := queryInt(t, pool, `SELECT count(*) FROM danmaku`)
	if bindings != 0 || rows != 0 {
		t.Errorf("库里有 %d 个绑定、%d 条弹幕，want 都没有", bindings, rows)
	}
}

// assertAppError err 是给定状态码与提示的 *errcode.Error。
func assertAppError(t *testing.T, err error, status int, message string) {
	t.Helper()
	appErr, ok := errors.AsType[*errcode.Error](err)
	if !ok || appErr.HTTPStatus != status || appErr.Message != message {
		t.Errorf("err = %v, want %d %q", err, status, message)
	}
}

func TestCreateBinding(t *testing.T) {
	tests := []struct {
		name        string
		fetched     source.Fetched
		wantDanmaku []danmaku.Danmaku // 按原始 ID 排序
		wantVersion int32
	}{
		{
			name:    "有弹幕：源内重复的只留一条，content_version 为 1",
			fetched: video,
			wantDanmaku: []danmaku.Danmaku{
				video.Danmaku[2], video.Danmaku[3], video.Danmaku[0],
			},
			wantVersion: 1,
		},
		{
			name:        "弹幕已关闭：0 条，content_version 为 0",
			fetched:     source.Fetched{Title: "关闭了弹幕的视频", Duration: 600},
			wantVersion: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var logs lockedBuffer
			adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": tt.fetched}}
			svc, pool := newBindingService(t, adapter, slogTo(&logs))

			got, err := svc.Create(t.Context(), 1, "fake/s1")
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			if got.LastFetchedAt == nil {
				t.Error("lastFetchedAt 为空，want 这次拉取的时间")
			}
			got.LastFetchedAt = nil
			want := BindingView{
				ID:           1,
				Adapter:      "fake",
				SourceURL:    "https://fake.test/s1",
				SourceLabel:  "假来源 s1",
				Title:        tt.fetched.Title,
				Duration:     int32(tt.fetched.Duration),
				Status:       "active",
				DanmakuCount: int32(len(tt.wantDanmaku)),
			}
			if got != want {
				t.Errorf("Create() = %+v\nwant %+v", got, want)
			}
			if items := readDanmaku(t, pool, got.ID); !slices.Equal(items, tt.wantDanmaku) {
				t.Errorf("落库的弹幕 = %+v\nwant %+v", items, tt.wantDanmaku)
			}
			if v := queryInt(t, pool, `SELECT content_version FROM bindings WHERE id = $1`, got.ID); v != int64(tt.wantVersion) {
				t.Errorf("content_version = %d, want %d", v, tt.wantVersion)
			}

			// 拉取结束的 info 日志：适配器的统计加上新增条数
			wantLog := `level=INFO msg="danmaku fetched" binding_id=1 adapter=fake`
			for _, attr := range tt.fetched.LogAttrs {
				wantLog += " " + attr.String()
			}
			wantLog += fmt.Sprintf(" added=%d", len(tt.wantDanmaku))
			if !strings.Contains(logs.String(), wantLog) {
				t.Errorf("日志 = %s\nwant 含 %s", logs.String(), wantLog)
			}
		})
	}
}

func TestCreateBindingFailed(t *testing.T) {
	tests := []struct {
		name        string
		episodeID   int64
		link        string
		err         error // 适配器 Fetch 返回的错误
		wantStatus  int
		wantMessage string
		wantFetches int32
	}{
		{
			name: "无法识别的链接", episodeID: 1, link: "https://example.com/v/1",
			wantStatus: http.StatusBadRequest, wantMessage: "无法识别的链接",
		},
		{
			name: "集不存在：不拉取", episodeID: 3, link: "fake/s1",
			wantStatus: http.StatusNotFound, wantMessage: "集不存在",
		},
		{
			name: "弹幕源不存在", episodeID: 1, link: "fake/gone",
			wantStatus: http.StatusUnprocessableEntity, wantMessage: "视频不存在、已删除或不可见", wantFetches: 1,
		},
		{
			name: "需要登录", episodeID: 1, link: "fake/s1",
			err:        &source.Error{Kind: source.AuthRequired, Message: "需要登录，请配置 SESSDATA", Err: errors.New("code -101")},
			wantStatus: http.StatusBadGateway, wantMessage: "需要登录，请配置 SESSDATA", wantFetches: 1,
		},
		{
			name: "限流", episodeID: 1, link: "fake/s1",
			err:        &source.Error{Kind: source.RateLimited, Message: "B 站限流，请稍后再试", Err: errors.New("HTTP 412")},
			wantStatus: http.StatusBadGateway, wantMessage: "B 站限流，请稍后再试", wantFetches: 1,
		},
		{
			name: "接口异常", episodeID: 1, link: "fake/s1",
			err:        &source.Error{Kind: source.Upstream, Message: "B 站接口异常", Err: errors.New("HTTP 503")},
			wantStatus: http.StatusBadGateway, wantMessage: "B 站接口异常", wantFetches: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}, err: tt.err}
			svc, pool := newBindingService(t, adapter, testLogger(t))

			_, err := svc.Create(t.Context(), tt.episodeID, tt.link)

			assertAppError(t, err, tt.wantStatus, tt.wantMessage)
			if tt.err != nil && !errors.Is(err, tt.err) {
				t.Errorf("err = %v, want 带着适配器的错误（底层原因进日志）", err)
			}
			if n := adapter.fetches.Load(); n != tt.wantFetches {
				t.Errorf("拉取了 %d 次，want %d", n, tt.wantFetches)
			}
			assertNothingWritten(t, pool)
		})
	}
}

// TestCreateBindingDuplicate 同一集用两种写法贴同一个弹幕源：第二次在拉取之前就返回 409；别的集照常可以绑定。
func TestCreateBindingDuplicate(t *testing.T) {
	t.Parallel()
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
	svc, pool := newBindingService(t, adapter, testLogger(t))

	if _, err := svc.Create(t.Context(), 1, "fake/s1"); err != nil {
		t.Fatalf("第一次 Create: %v", err)
	}
	_, err := svc.Create(t.Context(), 1, "alias/s1")
	assertAppError(t, err, http.StatusConflict, "这一集已经绑定过这个来源")
	if n := adapter.fetches.Load(); n != 1 {
		t.Errorf("拉取了 %d 次，want 1：重复的不拉取", n)
	}

	if _, err := svc.Create(t.Context(), 2, "alias/s1"); err != nil {
		t.Errorf("另一集绑定同一个弹幕源：%v", err)
	}
	if n := queryInt(t, pool, `SELECT count(*) FROM bindings`); n != 2 {
		t.Errorf("库里有 %d 个绑定，want 2", n)
	}
}

// TestCreateBindingTimeout 上游一直不响应：拉取到总时限 fetchTimeout 时按 Upstream 返回 502，库里什么都不留。
// 在 synctest 气泡里用假时间，不真等 25 秒。
func TestCreateBindingTimeout(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		seedEpisodes(t, pool)
		svc := NewBindingService(repository.NewStore(pool), source.NewRegistry(&fakeAdapter{hang: true}), testLogger(t))

		start := time.Now()
		_, err := svc.Create(t.Context(), 1, "fake/s1")

		assertAppError(t, err, http.StatusBadGateway, "B 站接口异常")
		if elapsed := time.Since(start); elapsed != fetchTimeout {
			t.Errorf("%v 后才失败，want 总时限 %v", elapsed, fetchTimeout)
		}
		assertNothingWritten(t, pool)
	})
}

// TestCreateBindingEpisodeDeleted 拉取期间这一集被删除：404"这一集已被删除"，库里不留绑定和弹幕。
func TestCreateBindingEpisodeDeleted(t *testing.T) {
	t.Parallel()
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
	adapter.gate()
	svc, pool := newBindingService(t, adapter, testLogger(t))

	errc := make(chan error, 1)
	go func() {
		_, err := svc.Create(t.Context(), 1, "fake/s1")
		errc <- err
	}()
	waitFetching(t, adapter, errc)
	if _, err := pool.Exec(t.Context(), `DELETE FROM episodes WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	close(adapter.release)

	assertAppError(t, <-errc, http.StatusNotFound, "这一集已被删除")
	assertNothingWritten(t, pool)
}

// TestCreateBindingConcurrently 同时两次给同一集贴同一个弹幕源：两次都过了拉取前的查重，
// 后写入的撞上唯一约束，一个成功、一个 409。
func TestCreateBindingConcurrently(t *testing.T) {
	t.Parallel()
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
	adapter.gate()
	svc, pool := newBindingService(t, adapter, testLogger(t))

	errc := make(chan error, 2)
	for _, link := range []string{"fake/s1", "alias/s1"} {
		go func() {
			_, err := svc.Create(t.Context(), 1, link)
			errc <- err
		}()
	}
	waitFetching(t, adapter, errc)
	waitFetching(t, adapter, errc)
	close(adapter.release)

	errs := []error{<-errc, <-errc}
	if errs[0] != nil {
		errs[0], errs[1] = errs[1], errs[0]
	}
	if errs[0] != nil {
		t.Errorf("两次都失败：%v；%v", errs[0], errs[1])
	}
	assertAppError(t, errs[1], http.StatusConflict, "这一集已经绑定过这个来源")
	if n := queryInt(t, pool, `SELECT count(*) FROM bindings`); n != 1 {
		t.Errorf("库里有 %d 个绑定，want 1", n)
	}
}

// videoV2 同一个弹幕源之后再拉取的结果：标题、时长变了；原始 ID 10 的弹幕已在 B 站删除，新增了 40、50。
var videoV2 = source.Fetched{
	Title:    "星海旅人 / 第 1 话（修正版）",
	Duration: 1420,
	Danmaku: []danmaku.Danmaku{
		video.Danmaku[3],
		video.Danmaku[0],
		{SourceID: 40, TimeMs: 300000, Mode: danmaku.ModeBottom, Color: 0x00FF00, Text: "新来的"},
		{SourceID: 50, TimeMs: 1200000, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "完结撒花"},
	},
	LogAttrs: []slog.Attr{slog.Int("protobuf", 4)},
}

// createS1 给集 1 绑定弹幕源 s1，返回绑定 ID。适配器这时返回 video 时，
// 绑定有原始 ID 为 10、20、30 的三条弹幕，content_version 为 1。
func createS1(t *testing.T, svc *BindingService) int64 {
	t.Helper()
	b, err := svc.Create(t.Context(), 1, "fake/s1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return b.ID
}

// getBinding 读出库里的绑定。
func getBinding(t *testing.T, pool *pgxpool.Pool, id int64) repository.Binding {
	t.Helper()
	b, err := repository.New(pool).GetBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("GetBinding(%d): %v", id, err)
	}
	return b
}

// sourceIDs 一个绑定落库的弹幕的原始 ID，升序。
func sourceIDs(t *testing.T, pool *pgxpool.Pool, bindingID int64) []int64 {
	t.Helper()
	items := readDanmaku(t, pool, bindingID)
	ids := make([]int64, len(items))
	for i, d := range items {
		ids[i] = d.SourceID
	}
	return ids
}

// TestRefetch 同一个绑定依次重新拉取、清空后重新拉取：新增条数、落库的弹幕与 content_version；
// 每次都用最新的标题和时长、更新拉取时间、记一条 info 日志，不覆盖偏移。
func TestRefetch(t *testing.T) {
	t.Parallel()
	var logs lockedBuffer
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
	svc, pool := newBindingService(t, adapter, slogTo(&logs))
	id := createS1(t, svc)
	if _, err := svc.UpdateOffset(t.Context(), id, 1.5); err != nil {
		t.Fatalf("UpdateOffset: %v", err)
	}
	closed := source.Fetched{Title: "关闭了弹幕的视频", Duration: 1418}

	steps := []struct {
		name        string
		fetched     source.Fetched
		replace     bool
		wantAdded   int64
		wantIDs     []int64
		wantVersion int32
	}{
		{"重新拉取：只插入新弹幕，B 站上删掉的继续保留", videoV2, false, 2, []int64{10, 20, 30, 40, 50}, 2},
		{"重新拉取没有新弹幕：content_version 不变", videoV2, false, 0, []int64{10, 20, 30, 40, 50}, 2},
		{"清空后重新拉取：恰好是这次的结果，新增条数为总条数", videoV2, true, 4, []int64{20, 30, 40, 50}, 3},
		{"清空后重新拉取结果没变：content_version 照样加 1", videoV2, true, 4, []int64{20, 30, 40, 50}, 4},
		{"清空后重新拉取，弹幕已关闭：0 条", closed, true, 0, nil, 5},
		{"重新拉取又有了弹幕", video, false, 3, []int64{10, 20, 30}, 6},
	}
	prev := getBinding(t, pool, id)
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			adapter.sources["s1"] = step.fetched

			got, added, err := svc.Refetch(t.Context(), id, step.replace)
			if err != nil {
				t.Fatalf("Refetch: %v", err)
			}

			if added != step.wantAdded {
				t.Errorf("added = %d, want %d", added, step.wantAdded)
			}
			if got.LastFetchedAt == nil || !got.LastFetchedAt.After(*prev.LastFetchedAt) {
				t.Errorf("lastFetchedAt = %v, want 晚于上次拉取的 %v", got.LastFetchedAt, prev.LastFetchedAt)
			}
			got.LastFetchedAt = nil
			want := BindingView{
				ID:           id,
				Adapter:      "fake",
				SourceURL:    "https://fake.test/s1",
				SourceLabel:  "假来源 s1",
				Title:        step.fetched.Title,
				Duration:     int32(step.fetched.Duration),
				Offset:       1.5,
				Status:       "active",
				DanmakuCount: int32(len(step.wantIDs)),
			}
			if got != want {
				t.Errorf("Refetch() = %+v\nwant %+v", got, want)
			}
			if ids := sourceIDs(t, pool, id); !slices.Equal(ids, step.wantIDs) {
				t.Errorf("落库的弹幕 = %v, want %v", ids, step.wantIDs)
			}
			prev = getBinding(t, pool, id)
			if prev.ContentVersion != step.wantVersion {
				t.Errorf("content_version = %d, want %d", prev.ContentVersion, step.wantVersion)
			}

			wantLog := `level=INFO msg="danmaku fetched" binding_id=1 adapter=fake`
			for _, attr := range step.fetched.LogAttrs {
				wantLog += " " + attr.String()
			}
			wantLog += fmt.Sprintf(" added=%d", step.wantAdded)
			if last := strings.TrimSpace(logs.String()); !strings.HasSuffix(last, wantLog) {
				t.Errorf("日志 = %s\nwant 最后一行以 %s 结尾", last, wantLog)
			}
		})
	}
}

// TestRefetchFailed 拉取失败：弹幕源不存在时把绑定标为失效、保留弹幕；其余错误什么都不改。
// 重新拉取与清空后重新拉取都一样。
func TestRefetchFailed(t *testing.T) {
	tests := []struct {
		name        string
		id          int64
		err         error // 适配器 Fetch 返回的错误
		wantStatus  int
		wantMessage string
		wantFetches int32
		wantDead    bool
	}{
		{
			name: "绑定不存在：不拉取", id: 2,
			wantStatus: http.StatusNotFound, wantMessage: "绑定不存在",
		},
		{
			name: "弹幕源不存在：标为失效", id: 1,
			err:        &source.Error{Kind: source.NotFound, Message: "视频不存在、已删除或不可见", Err: errors.New("code -404")},
			wantStatus: http.StatusUnprocessableEntity, wantMessage: "视频不存在、已删除或不可见", wantFetches: 1, wantDead: true,
		},
		{
			name: "需要登录", id: 1,
			err:        &source.Error{Kind: source.AuthRequired, Message: "需要登录，请配置 SESSDATA", Err: errors.New("code -101")},
			wantStatus: http.StatusBadGateway, wantMessage: "需要登录，请配置 SESSDATA", wantFetches: 1,
		},
		{
			name: "限流", id: 1,
			err:        &source.Error{Kind: source.RateLimited, Message: "B 站限流，请稍后再试", Err: errors.New("HTTP 412")},
			wantStatus: http.StatusBadGateway, wantMessage: "B 站限流，请稍后再试", wantFetches: 1,
		},
		{
			name: "接口异常", id: 1,
			err:        &source.Error{Kind: source.Upstream, Message: "B 站接口异常", Err: errors.New("HTTP 503")},
			wantStatus: http.StatusBadGateway, wantMessage: "B 站接口异常", wantFetches: 1,
		},
	}
	for _, tt := range tests {
		for _, replace := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/replace=%v", tt.name, replace), func(t *testing.T) {
				t.Parallel()
				adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
				svc, pool := newBindingService(t, adapter, testLogger(t))
				id := createS1(t, svc)
				before := getBinding(t, pool, id)
				adapter.sources["s1"] = videoV2 // 万一拉取成功了，会多出新弹幕
				adapter.err = tt.err
				adapter.fetches.Store(0)

				_, _, err := svc.Refetch(t.Context(), tt.id, replace)

				assertAppError(t, err, tt.wantStatus, tt.wantMessage)
				if tt.err != nil && !errors.Is(err, tt.err) {
					t.Errorf("err = %v, want 带着适配器的错误（底层原因进日志）", err)
				}
				if n := adapter.fetches.Load(); n != tt.wantFetches {
					t.Errorf("拉取了 %d 次，want %d", n, tt.wantFetches)
				}
				after := getBinding(t, pool, id)
				want := before
				if tt.wantDead {
					// 只改状态和拉取时间；弹幕、计数、标题、时长、content_version 都不动
					if !after.LastFetchedAt.After(*before.LastFetchedAt) {
						t.Errorf("last_fetched_at = %v, want 晚于创建时的 %v", after.LastFetchedAt, before.LastFetchedAt)
					}
					want.Status, want.LastFetchedAt, want.UpdatedAt = "dead", after.LastFetchedAt, after.UpdatedAt
				}
				if !reflect.DeepEqual(after, want) {
					t.Errorf("绑定 = %+v\nwant %+v", after, want)
				}
				if ids := sourceIDs(t, pool, id); !slices.Equal(ids, []int64{10, 20, 30}) {
					t.Errorf("落库的弹幕 = %v, want 不变", ids)
				}
			})
		}
	}
}

// TestRefetchDeadAndRecover 弹幕源不存在时标为失效；失效期间遇到临时错误，状态不变；弹幕源恢复后拉取成功即恢复正常。
func TestRefetchDeadAndRecover(t *testing.T) {
	t.Parallel()
	var logs lockedBuffer
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
	svc, pool := newBindingService(t, adapter, slogTo(&logs))
	id := createS1(t, svc)
	// 标为失效时记一条 info 日志，带上适配器给的原因
	wantLog := `level=INFO msg="binding marked dead" binding_id=1 adapter=fake reason="视频不存在、已删除或不可见: code -404"`

	adapter.err = &source.Error{Kind: source.NotFound, Message: "视频不存在、已删除或不可见", Err: errors.New("code -404")}
	_, _, err := svc.Refetch(t.Context(), id, false)
	assertAppError(t, err, http.StatusUnprocessableEntity, "视频不存在、已删除或不可见")
	if b := getBinding(t, pool, id); b.Status != "dead" || b.DanmakuCount != 3 {
		t.Errorf("状态 %s、%d 条弹幕，want 失效、保留 3 条", b.Status, b.DanmakuCount)
	}
	if !strings.Contains(logs.String(), wantLog) {
		t.Errorf("日志 = %s\nwant 含 %s", logs.String(), wantLog)
	}

	adapter.err = &source.Error{Kind: source.Upstream, Message: "B 站接口异常"}
	_, _, err = svc.Refetch(t.Context(), id, true)
	assertAppError(t, err, http.StatusBadGateway, "B 站接口异常")
	if b := getBinding(t, pool, id); b.Status != "dead" || b.DanmakuCount != 3 {
		t.Errorf("状态 %s、%d 条弹幕，want 仍然失效、保留 3 条", b.Status, b.DanmakuCount)
	}
	if n := strings.Count(logs.String(), "binding marked dead"); n != 1 {
		t.Errorf("标为失效的日志有 %d 条，want 1：临时错误不改状态，也不记", n)
	}

	adapter.err = nil
	adapter.sources["s1"] = videoV2
	got, added, err := svc.Refetch(t.Context(), id, false)
	if err != nil {
		t.Fatalf("Refetch: %v", err)
	}
	if got.Status != "active" || added != 2 || got.DanmakuCount != 5 {
		t.Errorf("状态 %s、新增 %d、共 %d 条，want 恢复正常、新增 2、共 5 条", got.Status, added, got.DanmakuCount)
	}
}

// TestRefetchTimeout 上游一直不响应：拉取到总时限 fetchTimeout 时按 Upstream 返回 502，绑定什么都不改。
func TestRefetchTimeout(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		seedEpisodes(t, pool)
		adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
		svc := NewBindingService(repository.NewStore(pool), source.NewRegistry(adapter), testLogger(t))
		id := createS1(t, svc)
		before := getBinding(t, pool, id)
		adapter.hang = true

		start := time.Now()
		_, _, err := svc.Refetch(t.Context(), id, true)

		assertAppError(t, err, http.StatusBadGateway, "B 站接口异常")
		if elapsed := time.Since(start); elapsed != fetchTimeout {
			t.Errorf("%v 后才失败，want 总时限 %v", elapsed, fetchTimeout)
		}
		if after := getBinding(t, pool, id); !reflect.DeepEqual(after, before) {
			t.Errorf("绑定 = %+v\nwant 不变 %+v", after, before)
		}
	})
}

func TestUpdateOffset(t *testing.T) {
	t.Parallel()
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
	svc, pool := newBindingService(t, adapter, testLogger(t))
	id := createS1(t, svc)
	before := getBinding(t, pool, id)

	got, err := svc.UpdateOffset(t.Context(), id, -2.5)
	if err != nil {
		t.Fatalf("UpdateOffset: %v", err)
	}
	if got.Offset != -2.5 || got.DanmakuCount != 3 {
		t.Errorf("UpdateOffset() = %+v, want 偏移 -2.5、3 条弹幕", got)
	}
	// 改偏移不改 content_version
	if after := getBinding(t, pool, id); after.Offset != -2.5 || after.ContentVersion != before.ContentVersion {
		t.Errorf("偏移 %v、content_version %d，want -2.5、%d", after.Offset, after.ContentVersion, before.ContentVersion)
	}

	_, err = svc.UpdateOffset(t.Context(), 2, 1)
	assertAppError(t, err, http.StatusNotFound, "绑定不存在")
}

// TestDeleteBinding 删除绑定，它的弹幕随之删除；同一个弹幕源在别的集上的绑定不受影响。
func TestDeleteBinding(t *testing.T) {
	t.Parallel()
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
	svc, pool := newBindingService(t, adapter, testLogger(t))
	id := createS1(t, svc)
	other, err := svc.Create(t.Context(), 2, "fake/s1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(t.Context(), id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if n := queryInt(t, pool, `SELECT count(*) FROM bindings WHERE id = $1`, id); n != 0 {
		t.Error("绑定还在")
	}
	if n := queryInt(t, pool, `SELECT count(*) FROM danmaku WHERE binding_id = $1`, id); n != 0 {
		t.Errorf("还留着它的 %d 条弹幕", n)
	}
	if ids := sourceIDs(t, pool, other.ID); len(ids) != 3 {
		t.Errorf("别的集上的绑定有 %d 条弹幕，want 3", len(ids))
	}

	assertAppError(t, svc.Delete(t.Context(), id), http.StatusNotFound, "绑定不存在")
}

// TestRefetchBindingDeleted 拉取期间绑定被删除：写回时返回 404"绑定已被删除"，拉取结果丢弃，库里不留它的弹幕。
// 弹幕源不存在、要标为失效时也一样。
func TestRefetchBindingDeleted(t *testing.T) {
	tests := []struct {
		name    string
		replace bool
		err     error // 适配器 Fetch 返回的错误
	}{
		{name: "重新拉取"},
		{name: "清空后重新拉取", replace: true},
		{name: "弹幕源不存在", err: &source.Error{Kind: source.NotFound, Message: "视频不存在、已删除或不可见"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
			svc, pool := newBindingService(t, adapter, testLogger(t))
			id := createS1(t, svc)
			adapter.sources["s1"] = videoV2
			adapter.err = tt.err
			adapter.gate()

			errc := make(chan error, 1)
			go func() {
				_, _, err := svc.Refetch(t.Context(), id, tt.replace)
				errc <- err
			}()
			waitFetching(t, adapter, errc)
			if _, err := pool.Exec(t.Context(), `DELETE FROM bindings WHERE id = $1`, id); err != nil {
				t.Fatal(err)
			}
			close(adapter.release)

			assertAppError(t, <-errc, http.StatusNotFound, "绑定已被删除")
			assertNothingWritten(t, pool)
		})
	}
}

// TestRefetchConcurrently 同一个绑定上的两次拉取同时写回：写入事务先锁住绑定，排队执行，两次都成功、不死锁。
// content_version 只在清空后重新拉取、或真正插入了新弹幕时加 1；danmaku_count 与实际条数一致（由 newBindingService 检查）。
func TestRefetchConcurrently(t *testing.T) {
	// 只少了原始 ID 10 的那条（B 站上删掉了），没有新弹幕：重新拉取不论先写回还是后写回，都插不进新弹幕
	trimmed := source.Fetched{
		Title:    video.Title,
		Duration: video.Duration,
		Danmaku:  []danmaku.Danmaku{video.Danmaku[0], video.Danmaku[3]},
	}
	// 期望不随写回的先后变化；创建时 content_version 为 1，弹幕为 10、20、30
	tests := []struct {
		name        string
		replaces    [2]bool
		fetched     source.Fetched
		wantIDs     []int64
		wantVersion int32
	}{
		// 先写回的插入 40、50，后写回的没有新弹幕
		{"两次重新拉取", [2]bool{false, false}, videoV2, []int64{10, 20, 30, 40, 50}, 2},
		{"重新拉取与清空后重新拉取", [2]bool{false, true}, trimmed, []int64{20, 30}, 2},
		{"两次清空后重新拉取", [2]bool{true, true}, videoV2, []int64{20, 30, 40, 50}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
			svc, pool := newBindingService(t, adapter, testLogger(t))
			id := createS1(t, svc)
			adapter.sources["s1"] = tt.fetched
			adapter.gate()

			errc := make(chan error, 2)
			for _, replace := range tt.replaces {
				go func() {
					_, _, err := svc.Refetch(t.Context(), id, replace)
					errc <- err
				}()
			}
			waitFetching(t, adapter, errc)
			waitFetching(t, adapter, errc)
			close(adapter.release)
			for range 2 {
				if err := <-errc; err != nil {
					t.Errorf("Refetch: %v", err)
				}
			}

			if v := getBinding(t, pool, id).ContentVersion; v != tt.wantVersion {
				t.Errorf("content_version = %d, want %d", v, tt.wantVersion)
			}
			if ids := sourceIDs(t, pool, id); !slices.Equal(ids, tt.wantIDs) {
				t.Errorf("落库的弹幕 = %v, want %v", ids, tt.wantIDs)
			}
		})
	}
}

// TestRefetchKeepsOffset 拉取期间改了偏移：写回只更新拉取相关的列，不覆盖新的偏移。
func TestRefetchKeepsOffset(t *testing.T) {
	t.Parallel()
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}
	svc, pool := newBindingService(t, adapter, testLogger(t))
	id := createS1(t, svc)
	adapter.gate()

	var got BindingView
	errc := make(chan error, 1)
	go func() {
		var err error
		got, _, err = svc.Refetch(t.Context(), id, true)
		errc <- err
	}()
	waitFetching(t, adapter, errc)
	if _, err := svc.UpdateOffset(t.Context(), id, 3.5); err != nil {
		t.Fatalf("UpdateOffset: %v", err)
	}
	close(adapter.release)

	if err := <-errc; err != nil {
		t.Fatalf("Refetch: %v", err)
	}
	if got.Offset != 3.5 {
		t.Errorf("Refetch() 的偏移 = %v, want 3.5", got.Offset)
	}
	if b := getBinding(t, pool, id); b.Offset != 3.5 {
		t.Errorf("库里的偏移 = %v, want 3.5", b.Offset)
	}
}
