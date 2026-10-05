package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/repository"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// syncTest 在 synctest 气泡里运行 f，f 拿到一个新库的连接池。
// 气泡里的时间是假的，synctest.Wait 等到后台的同步结束、Run 回到等待状态，测试不靠 sleep 等时序。
// 连接池在气泡里创建、在气泡里关闭：pgx 连接内部的 channel 不能跨气泡使用。
// f 结束时检查不变量（assertInvariants）。
func syncTest(t *testing.T, f func(t *testing.T, pool *pgxpool.Pool)) {
	t.Helper()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		f(t, pool)
		assertInvariants(t, pool)
	})
}

// assertInvariants 检查任何时候都成立的不变量：每个绑定的 danmaku_count 等于它实际的弹幕条数；
// images 表里没有不被任何剧引用的图片；每条处理过的记录都指向存在的集；
// 带 season_binding_id 的绑定，所在的集属于那个季绑定的季。
func assertInvariants(t *testing.T, pool *pgxpool.Pool) {
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

// newTestService 构造不开定时同步的 SyncService 并在后台运行 Run。src 为 nil 表示未配置目录源。
func newTestService(t *testing.T, pool *pgxpool.Pool, src catalog.Source) *SyncService {
	t.Helper()
	svc := NewSyncService(repository.NewStore(pool), pool, src, config.Sync{}, testLogger(t))
	runInBackground(t, svc)
	return svc
}

// runInBackground 在后台运行 svc.Run，等它做完启动时的清理再返回。
// 返回的 stop 取消 Run 并等它返回；测试结束时也会自动调用。
func runInBackground(t *testing.T, svc interface{ Run(context.Context) }) (stop func()) {
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

// testLogger 日志写进测试输出，只在失败或 -v 时显示。
func testLogger(t *testing.T) *slog.Logger {
	return slogTo(t.Output())
}

func slogTo(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, nil))
}

// holdSyncLock 模拟另一个实例正在同步：拿走同步的租约。返回的 unlock 可以重复调用，测试结束时也会自动调用。
func holdSyncLock(t *testing.T, pool *pgxpool.Pool) (unlock func()) {
	t.Helper()
	lease, ok, err := database.TryLease(t.Context(), pool, testLogger(t), database.LeaseSync)
	if err != nil || !ok {
		t.Fatalf("TryLease = %v, %v", ok, err)
	}
	t.Cleanup(lease.Release) // 在关闭连接池之前：释放要用连接池
	return lease.Release
}

// syncOnce 手动触发一次同步，等它结束后返回同步记录。
func syncOnce(t *testing.T, svc *SyncService) repository.SyncRun {
	t.Helper()
	return getRun(t, svc, triggerSync(t, svc))
}

// triggerSync 手动触发一次同步，等后台停下（同步结束，或停在假目录源的 gate 上），返回这次同步的 ID。
func triggerSync(t *testing.T, svc *SyncService) int64 {
	t.Helper()
	id, err := svc.Trigger(t.Context())
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	synctest.Wait()
	return id
}

func getRun(t *testing.T, svc *SyncService, id int64) repository.SyncRun {
	t.Helper()
	run, err := svc.GetRun(t.Context(), id)
	if err != nil {
		t.Fatalf("GetRun(%d): %v", id, err)
	}
	return run
}

// fakeSource 实现 catalog.Source 的假目录源：List 返回 items 的清单，Items 依次产出 items。
type fakeSource struct {
	warnings []string
	items    []catalog.Item
	listErr  error         // List 直接返回这个错误
	failAt   int           // 大于 0 时，第 failAt 项（从 1 数起）改为产出请求失败的错误
	gate     chan struct{} // 不为 nil 时，每产出一项之前等一次放行
}

var errSourceRequest = errors.New("目录源请求失败")

func (f *fakeSource) List(ctx context.Context) (catalog.Listing, error) {
	if f.listErr != nil {
		return catalog.Listing{}, f.listErr
	}
	items := f.items
	return catalog.Listing{
		Total:    len(items),
		Warnings: f.warnings,
		Items: func(yield func(catalog.Item, error) bool) {
			for i, item := range items {
				if f.gate != nil {
					select {
					case <-f.gate:
					case <-ctx.Done(): // 与真实的适配一样：ctx 取消时请求失败
						yield(catalog.Item{}, ctx.Err())
						return
					}
				}
				if i+1 == f.failAt {
					yield(catalog.Item{}, errSourceRequest)
					return
				}
				if !yield(item, nil) {
					return
				}
			}
		},
	}, nil
}

func tv(title string, year *int, seasons ...catalog.Season) *catalog.Series {
	return &catalog.Series{Type: catalog.TypeTV, Title: title, Year: year, Seasons: seasons}
}

func movie(title string, year *int, duration int) *catalog.Series {
	return &catalog.Series{Type: catalog.TypeMovie, Title: title, Year: year, Seasons: []catalog.Season{
		{Number: 1, Episodes: []catalog.Episode{{Number: 1, Duration: &duration}}},
	}}
}

func season(number int, title string, episodes ...catalog.Episode) catalog.Season {
	return catalog.Season{Number: number, Title: title, Episodes: episodes}
}

func episode(number int, title string, duration int) catalog.Episode {
	return catalog.Episode{Number: number, Title: title, Duration: &duration}
}

// item 目录源产出的一部剧，Name 取剧名。
func item(s *catalog.Series, warnings ...string) catalog.Item {
	return catalog.Item{Name: s.Title, Series: s, Warnings: warnings}
}

// catalogRow 目录里的一集，连同它所在的季和剧。
type catalogRow struct {
	ids  [3]int64 // 剧、季、集的 id
	text string   // "类型|剧名|原名|年份 / 季号|季标题 / 集号|集标题|时长"，空值写作 -
}

// readCatalog 读出目录里的全部集，按剧、季、集的 id 排序。
func readCatalog(t *testing.T, pool *pgxpool.Pool) []catalogRow {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT s.id, se.id, e.id,
		       format('%s|%s|%s|%s / %s|%s / %s|%s|%s',
		              s.type, s.title, coalesce(s.original_title, '-'), coalesce(s.year::text, '-'),
		              se.number, coalesce(se.title, '-'),
		              e.number, coalesce(e.title, '-'), coalesce(e.duration::text, '-'))
		FROM series s
		JOIN seasons se ON se.series_id = s.id
		JOIN episodes e ON e.season_id = se.id
		ORDER BY s.id, se.id, e.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []catalogRow
	for rows.Next() {
		var r catalogRow
		if err := rows.Scan(&r.ids[0], &r.ids[1], &r.ids[2], &r.text); err != nil {
			t.Fatal(err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func texts(rows []catalogRow) []string {
	result := make([]string, len(rows))
	for i, r := range rows {
		result[i] = r.text
	}
	return result
}

// poster 一部剧的海报：图片 ID（没有海报时为 0），以及 "content-type|内容"（没有海报时为空）。
type poster struct {
	id   int64
	text string
}

// readPoster 读出剧名为 title 的那部剧（目录里只有一部同名的剧）的海报。
func readPoster(t *testing.T, pool *pgxpool.Pool, title string) poster {
	t.Helper()
	var p poster
	err := pool.QueryRow(t.Context(), `
		SELECT coalesce(i.id, 0), coalesce(i.content_type || '|' || convert_from(i.data, 'UTF8'), '')
		FROM series s
		LEFT JOIN images i ON i.id = s.poster_image_id
		WHERE s.title = $1`, title).Scan(&p.id, &p.text)
	if err != nil {
		t.Fatalf("读出「%s」的海报：%v", title, err)
	}
	return p
}

// runCounts 同步记录的状态、进度与新增数，例如 "succeeded 2/2 新增剧 2 季 3 集 4"，便于整体比较。
func runCounts(run repository.SyncRun) string {
	total := "-"
	if run.Total != nil {
		total = fmt.Sprint(*run.Total)
	}
	return fmt.Sprintf("%s %s/%d 新增剧 %d 季 %d 集 %d", run.Status, total, run.Done,
		run.CreatedSeries, run.CreatedSeasons, run.CreatedEpisodes)
}

// lockedBuffer 可以在多个 goroutine 里写的日志缓冲。
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
