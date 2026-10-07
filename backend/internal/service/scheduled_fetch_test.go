package service

import (
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// testScheduledFetch 定时拉取的时间规则，取配置项的默认值。
var testScheduledFetch = config.Defaults().ScheduledFetch

// fetchEnv 定时拉取测试的环境：两集（seedEpisodes），源适配器只注册了 src，ScheduledFetchService 在后台运行。
type fetchEnv struct {
	t        *testing.T
	pool     *pgxpool.Pool
	bindings *BindingService
	logs     lockedBuffer // 服务的日志，同时写进测试输出
}

// newFetchEnv 在 synctest 气泡里调用，pool 是气泡里新建的连接池。
func newFetchEnv(t *testing.T, pool *pgxpool.Pool, src *fakeCollector) *fetchEnv {
	t.Helper()
	seedEpisodes(t, pool)
	e := &fetchEnv{t: t, pool: pool}
	logger := slogTo(io.MultiWriter(t.Output(), &e.logs))
	store := repository.NewStore(pool)
	e.bindings = NewBindingService(store, source.NewRegistry(src), logger)
	runInBackground(t, NewScheduledFetchService(store, pool, e.bindings, testScheduledFetch, logger))
	return e
}

// bind 在第 1 集上建出弹幕源 name 的绑定（还没有弹幕），建出时间为 createdAt，上次拉取与尝试拉取的时间为 attemptedAt，返回绑定 ID。
func (e *fetchEnv) bind(name string, createdAt, attemptedAt time.Time) int64 {
	e.t.Helper()
	var id int64
	err := e.pool.QueryRow(e.t.Context(), `
		INSERT INTO bindings (episode_id, adapter, ref, title, duration, created_at, last_fetched_at, fetch_attempted_at)
		VALUES (1, 'fake', jsonb_build_object('name', $1::text), '弹幕源 ' || $1, 1420, $2, $3, $3)
		RETURNING id`, name, createdAt, attemptedAt).Scan(&id)
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// TestScheduledFetch 绑定距上次尝试拉取满 12 小时就定时拉取一次，不论它是怎么建出的；手动重新拉取也把下一次推后。
func TestScheduledFetch(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{videos: fakeVideos("a", "b")}
		env := newFetchEnv(t, pool, src)
		start := time.Now()
		a := env.bind("a", start, start)
		b := env.bind("b", start, start)

		time.Sleep(testScheduledFetch.Interval / 2)
		synctest.Wait()
		if _, _, err := env.bindings.Refetch(t.Context(), b, false); err != nil {
			t.Fatalf("Refetch: %v", err)
		}
		assertStrings(t, "6 小时后的拉取", src.fetchedNames(), []string{"b"})

		time.Sleep(testScheduledFetch.Interval / 2)
		synctest.Wait()
		assertStrings(t, "12 小时后的拉取", src.fetchedNames(), []string{"b", "a"})
		got := getBinding(t, pool, a)
		if want := start.Add(testScheduledFetch.Interval); !got.LastFetchedAt.Equal(want) || !got.FetchAttemptedAt.Equal(want) {
			t.Errorf("上次拉取 %v、尝试拉取 %v，want 都是 %v", got.LastFetchedAt, got.FetchAttemptedAt, want)
		}
		if got.DanmakuCount != 2 {
			t.Errorf("弹幕 %d 条，want 2", got.DanmakuCount)
		}

		time.Sleep(testScheduledFetch.Interval / 2)
		synctest.Wait()
		assertStrings(t, "18 小时后的拉取", src.fetchedNames(), []string{"b", "a", "b"})

		time.Sleep(testScheduledFetch.Interval / 2)
		synctest.Wait()
		assertStrings(t, "24 小时后的拉取", src.fetchedNames(), []string{"b", "a", "b", "a"})
	})
}

// TestScheduledFetchWindow 只拉取建出不到 14 天、距上次尝试拉取满 12 小时、能重新拉取的绑定；弹幕源不存在时标为失效。
func TestScheduledFetchWindow(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{videos: fakeVideos("a", "b", "c")}
		env := newFetchEnv(t, pool, src)
		now := time.Now()
		days13, overdue := now.Add(-13*24*time.Hour), now.Add(-testScheduledFetch.Interval-time.Hour)
		env.bind("a", now.Add(-testScheduledFetch.Window), overdue)            // 建出满 14 天
		env.bind("b", days13, overdue)                                         // 到期
		env.bind("c", days13, now.Add(-testScheduledFetch.Interval+time.Hour)) // 还差一小时满 12 小时
		d := env.bind("d", days13, overdue)                                    // 到期，但弹幕源已不存在
		// 用弹幕文件建的绑定不能重新拉取，没有尝试拉取的时间
		if _, err := pool.Exec(t.Context(), `INSERT INTO bindings (episode_id, kind, title, created_at) VALUES (1, 'file', '弹幕文件', $1)`, days13); err != nil {
			t.Fatal(err)
		}

		time.Sleep(scheduledFetchScanInterval)
		synctest.Wait()
		assertStrings(t, "拉取", src.fetchedNames(), []string{"b", "d"})
		if b := getBinding(t, pool, d); b.Status != "dead" {
			t.Errorf("d 的状态 = %s, want dead", b.Status)
		}
	})
}

// TestScheduledFetchFailed 拉取失败的绑定等满 12 小时再试，不每次扫描都重试；限流时结束这一轮，下一次扫描接着拉取剩下的。
func TestScheduledFetchFailed(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCollector{
			videos: fakeVideos("a", "b", "c"),
			fetchErrs: map[string]error{
				"a": &source.Error{Kind: source.Upstream, Message: "B 站接口异常"},
				"b": &source.Error{Kind: source.RateLimited, Message: "B 站限流，请稍后再试"},
			},
		}
		env := newFetchEnv(t, pool, src)
		now := time.Now()
		created := now.Add(-5 * 24 * time.Hour)
		env.bind("a", created, now.Add(-3*24*time.Hour))
		env.bind("b", created, now.Add(-2*24*time.Hour))
		env.bind("c", created, now.Add(-24*time.Hour))

		time.Sleep(scheduledFetchScanInterval)
		synctest.Wait()
		assertStrings(t, "第一轮", src.fetchedNames(), []string{"a", "b"})
		if want := `level=WARN msg="scheduled fetch finished"`; !strings.Contains(env.logs.String(), want) {
			t.Errorf("日志 = %s\nwant 含 %s", env.logs.String(), want)
		}

		time.Sleep(scheduledFetchScanInterval)
		synctest.Wait()
		assertStrings(t, "第二轮", src.fetchedNames(), []string{"a", "b", "c"})

		src.fetchErrs = nil
		time.Sleep(testScheduledFetch.Interval)
		synctest.Wait()
		assertStrings(t, "12 小时后", src.fetchedNames(), []string{"a", "b", "c", "a", "b", "c"})
	})
}
