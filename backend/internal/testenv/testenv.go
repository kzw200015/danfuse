// Package testenv 测试用的环境：在 dbtest 的独立库上按 app.New 的依赖关系组装各领域的 service（New），
// 以及各领域的测试共用的辅助函数和假适配器：假目录源 FakeCatalog（catalog.go）、有合集的假源适配器 FakeCollector（source.go）。
// 用到数据库的测试包仍要在 TestMain 里调用 dbtest.Main(m)：
//
//	pool := dbtest.Pool(t)
//	testenv.SeedEpisodes(t, pool)
//	env := testenv.New(pool, testenv.Logger(t), adapter)
package testenv

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/dandan"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// Env 按 app.New 的依赖关系组装好的各领域 service。后台循环（同步、补建、定时拉取）都不运行，
// 要它们时测试自己构造、用 RunInBackground 运行。
type Env struct {
	Pool           *pgxpool.Pool
	Sources        *source.Registry
	Bindings       *binding.Service
	SeasonBindings *seasonbinding.Service // 追更的时间规则取配置项的默认值
	Catalog        *catalog.Service
	Dandan         *dandan.Service
}

// New 在 pool 上组装各领域的 service，源适配器只注册 adapters。
func New(pool *pgxpool.Pool, logger *slog.Logger, adapters ...source.Adapter) *Env {
	sources := source.NewRegistry(adapters...)
	bindings := binding.NewService(pool, sources, logger)
	seasonBindings := seasonbinding.NewService(pool, sources, bindings, config.Defaults().Follow, logger)
	return &Env{
		Pool:           pool,
		Sources:        sources,
		Bindings:       bindings,
		SeasonBindings: seasonBindings,
		Catalog:        catalog.NewService(pool, bindings, seasonBindings),
		Dandan:         dandan.NewService(pool, bindings),
	}
}

// SyncTest 在 synctest 气泡里运行 f，f 拿到一个新库的连接池。
// 气泡里的时间是假的，synctest.Wait 等到后台的任务结束、Run 回到等待状态，测试不靠 sleep 等时序。
// 连接池在气泡里创建、在气泡里关闭：pgx 连接内部的 channel 不能跨气泡使用。
// f 结束时检查不变量（AssertInvariants）。
func SyncTest(t *testing.T, f func(t *testing.T, pool *pgxpool.Pool)) {
	t.Helper()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		pool := dbtest.Open(t, cfg)
		f(t, pool)
		AssertInvariants(t, pool)
	})
}

// AssertInvariants 检查任何时候都成立的不变量：每个绑定的 danmaku_count 等于它实际的弹幕条数、
// max_time_ms 等于它最晚一条弹幕的时间（不早于 0）、file_count 等于它的弹幕文件份数；
// images 表里没有不被任何剧引用的图片；每条处理过的记录都指向存在的集；
// 带 season_binding_id 的绑定，所在的集属于那个季绑定的季。
func AssertInvariants(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background() // 在 t.Cleanup 里调用时 t.Context() 已经取消
	rows, err := pool.Query(ctx, `
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

	var badMaxTimes []int64
	err = pool.QueryRow(ctx, `
		SELECT coalesce(array_agg(b.id ORDER BY b.id), '{}')
		FROM bindings b
		WHERE b.max_time_ms <> GREATEST(0, (SELECT max(time_ms) FROM danmaku d WHERE d.binding_id = b.id))`).Scan(&badMaxTimes)
	if err != nil {
		t.Fatal(err)
	}
	if len(badMaxTimes) > 0 {
		t.Errorf("绑定 %v 的 max_time_ms 与实际最晚一条弹幕的时间不符", badMaxTimes)
	}

	var badFileCounts []int64
	err = pool.QueryRow(ctx, `
		SELECT coalesce(array_agg(b.id ORDER BY b.id), '{}')
		FROM bindings b
		WHERE b.file_count <> (SELECT count(*) FROM binding_files f WHERE f.binding_id = b.id)`).Scan(&badFileCounts)
	if err != nil {
		t.Fatal(err)
	}
	if len(badFileCounts) > 0 {
		t.Errorf("绑定 %v 的 file_count 与实际的弹幕文件份数不符", badFileCounts)
	}

	var orphans []int64
	err = pool.QueryRow(ctx, `
		SELECT coalesce(array_agg(id ORDER BY id), '{}')
		FROM images i
		WHERE NOT EXISTS (SELECT 1 FROM series s WHERE s.poster_image_id = i.id)`).Scan(&orphans)
	if err != nil {
		t.Fatal(err)
	}
	if len(orphans) > 0 {
		t.Errorf("images 里有不被任何剧引用的图片：%v", orphans)
	}

	var dangling int64
	err = pool.QueryRow(ctx, `
		SELECT count(*)
		FROM season_binding_handled h
		WHERE NOT EXISTS (SELECT 1 FROM episodes e WHERE e.id = h.episode_id)`).Scan(&dangling)
	if err != nil {
		t.Fatal(err)
	}
	if dangling > 0 {
		t.Errorf("有 %d 条处理过的记录指向不存在的集", dangling)
	}

	var misplaced []int64
	err = pool.QueryRow(ctx, `
		SELECT coalesce(array_agg(b.id ORDER BY b.id), '{}')
		FROM bindings b
		JOIN episodes e ON e.id = b.episode_id
		JOIN season_bindings sb ON sb.id = b.season_binding_id
		WHERE e.season_id <> sb.season_id`).Scan(&misplaced)
	if err != nil {
		t.Fatal(err)
	}
	if len(misplaced) > 0 {
		t.Errorf("绑定 %v 所在的集不属于建出它的季绑定的季", misplaced)
	}
}

// RunInBackground 在后台运行 svc.Run，等它做完启动时的清理再返回（要在 synctest 的气泡里调用）。
// 返回的 stop 取消 Run 并等它返回；测试结束时也会自动调用。
func RunInBackground(t testing.TB, svc interface{ Run(context.Context) }) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx)
	}()
	stop = sync.OnceFunc(func() {
		cancel()
		<-done
	})
	t.Cleanup(stop) // 在关闭连接池之前
	synctest.Wait()
	return stop
}

// Logger 日志写进测试输出，只在失败或 -v 时显示。
func Logger(t testing.TB) *slog.Logger {
	return SlogTo(t.Output())
}

// SlogTo 文本格式的日志写进 w。
func SlogTo(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, nil))
}

// SeedEpisodes 写入一部剧、一季、两集，集 ID 为 1、2。
func SeedEpisodes(t testing.TB, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO series (type, title) VALUES ('tv', '星海旅人');
		INSERT INTO seasons (series_id, number) VALUES (1, 1);
		INSERT INTO episodes (season_id, number, duration) VALUES (1, 1, 1420), (1, 2, 1440);`)
	if err != nil {
		t.Fatal(err)
	}
}

// QueryInt 执行只返回一个整数的查询。
func QueryInt(t testing.TB, pool *pgxpool.Pool, sql string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

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

// LockedBuffer 可以在多个 goroutine 里写的日志缓冲。
type LockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *LockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *LockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
