package service_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	bindingsvc "github.com/kzw200015/danfuse/backend/internal/binding/service"
	"github.com/kzw200015/danfuse/backend/internal/danmakufile"
	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	"github.com/kzw200015/danfuse/backend/internal/infra/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// entry 合集里序号为 n 的条目，弹幕源名字为 name。
func entry(name string, n int) source.CollectionItem {
	return source.CollectionItem{Ref: source.Ref(`{"name":"` + name + `"}`), Number: n, Label: name}
}

// odd 平台没给出整数集号的条目。
func odd(name, reason string) source.CollectionItem {
	return source.CollectionItem{Ref: source.Ref(`{"name":"` + name + `"}`), Unmatched: reason, Label: name}
}

// labeled 按规则编号的合集里标签为 label 的条目，弹幕源名字为 name。
func labeled(name, label string) source.CollectionItem {
	return source.CollectionItem{Ref: source.Ref(`{"name":"` + name + `"}`), Label: label}
}

// parseRule 由一组正则得到集号规则。
func parseRule(t *testing.T, patterns ...string) source.EpisodeRule {
	t.Helper()
	r, err := source.ParseEpisodeRule(patterns)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// seasonEnv 季绑定测试的环境：一部剧的第 1 季（季 ID 1）有给定集号的集，第 2 季（季 ID 2）没有集；
// 源适配器只注册了 src；seasonbinding.Service 在后台运行。
type seasonEnv struct {
	t    *testing.T
	pool *pgxpool.Pool
	src  *testenv.FakeCollector
	svc  *service.Service
	// bindingSvc 与 svc 共用的 binding.Service，查看建出的绑定、写入文件夹的槽位用
	bindingSvc *bindingsvc.Service
	stop       func()               // 取消 Run 并等它返回
	logs       testenv.LockedBuffer // 服务的日志，同时写进测试输出
}

// testFollow 追更的时间规则，取配置项的默认值。
var testFollow = config.Defaults().Follow

// newSeasonEnv 在 synctest 气泡里调用，pool 是气泡里新建的连接池。集的建出时间为现在（假时间），与追更比较的时间一致。
func newSeasonEnv(t *testing.T, pool *pgxpool.Pool, src *testenv.FakeCollector, episodes ...int) *seasonEnv {
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

// start 构造新的 seasonbinding.Service 并在后台运行，像重启了服务。
func (e *seasonEnv) start() {
	sources := source.NewRegistry(e.src)
	logger := testenv.SlogTo(io.MultiWriter(e.t.Output(), &e.logs))
	e.bindingSvc = bindingsvc.NewService(e.pool, sources, logger)
	e.svc = service.NewService(e.pool, sources, e.bindingSvc, testFollow, logger)
	e.stop = testenv.RunInBackground(e.t, e.svc)
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

// collectionForm 合集的表单：链接 link，合集第 1 集为本地第 1 集，默认的集号规则。
func collectionForm(link string) service.CollectionForm {
	return service.CollectionForm{Link: link, Mapping: source.Mapping{From: 1, To: 1}, Rule: source.DefaultEpisodeRule()}
}

// create 用链接 list/s 给第 1 季创建合集的季绑定（合集第 1 集为第 1 集），等后台的补建结束，返回创建时的详情。
func (e *seasonEnv) create() service.Detail {
	e.t.Helper()
	return e.createWith(collectionForm("list/s"))
}

// createWith 用表单 f 给第 1 季创建合集的季绑定，等后台的补建结束，返回创建时的详情。
func (e *seasonEnv) createWith(f service.CollectionForm) service.Detail {
	e.t.Helper()
	d := e.createAsyncWith(f)
	synctest.Wait()
	return d
}

// createAsync 用链接 link 给第 1 季创建合集的季绑定（合集第 1 集为第 1 集），不等后台的补建，返回创建时的详情。
func (e *seasonEnv) createAsync(link string) service.Detail {
	e.t.Helper()
	return e.createAsyncWith(collectionForm(link))
}

// createAsyncWith 用表单 f 给第 1 季创建合集的季绑定，不等后台的补建，返回创建时的详情。
func (e *seasonEnv) createAsyncWith(f service.CollectionForm) service.Detail {
	e.t.Helper()
	d, err := e.svc.CreateCollection(e.t.Context(), 1, f)
	if err != nil {
		e.t.Fatalf("CreateCollection: %v", err)
	}
	return d
}

// backfill 立即补建一次，等它结束。
func (e *seasonEnv) backfill(id int64) {
	e.t.Helper()
	if _, err := e.svc.Backfill(e.t.Context(), id); err != nil {
		e.t.Fatalf("Backfill(%d): %v", id, err)
	}
	synctest.Wait()
}

func (e *seasonEnv) get(id int64) service.Detail {
	e.t.Helper()
	d, err := e.svc.Get(e.t.Context(), id)
	if err != nil {
		e.t.Fatalf("Get(%d): %v", id, err)
	}
	return d
}

// setFollow 开关追更。
func (e *seasonEnv) setFollow(id int64, follow bool) {
	e.t.Helper()
	if _, err := e.svc.SetFollow(e.t.Context(), id, follow); err != nil {
		e.t.Fatalf("SetFollow(%d, %v): %v", id, follow, err)
	}
}

// bindings 库里的全部绑定："集号 弹幕源名字 季绑定"，文件绑定没有弹幕源、写作标题，手动建的季绑定写作 -，按集号、绑定 ID 排序。
func (e *seasonEnv) bindings() []string {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `
		SELECT format('%s %s %s', e.number, coalesce(b.ref->>'name', b.title), coalesce(b.season_binding_id::text, '-'))
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

// bindManually 在第 1 季的第 number 集上手动绑定弹幕源 name（没有季绑定）。
func (e *seasonEnv) bindManually(number int, name string) {
	e.t.Helper()
	e.exec(`
		INSERT INTO bindings (episode_id, adapter, ref, title, duration)
		SELECT id, 'fake', jsonb_build_object('name', $2::text), '弹幕源 ' || $2, 1420
		FROM episodes
		WHERE season_id = 1 AND number = $1`, number, name)
}

// folder 直接写入第 1 季的文件夹的季绑定"星海旅人"（默认规则、1 = 1），条目各一份弹幕文件；
// slots 为条目下标 → 集号，按它在那一集建出槽位（不经过派生）。返回季绑定 ID。
func (e *seasonEnv) folder(labels []string, slots map[int]int) int64 {
	e.t.Helper()
	entries := make([]testenv.FolderEntry, len(labels))
	for i, label := range labels {
		entries[i] = testenv.FolderEntry{Label: label, Files: []danmakufile.File{testenv.Snapshot1(label + ".xml")}}
	}
	id, entryIDs := testenv.InsertFolderSeasonBinding(e.t, e.pool, 1, "星海旅人", 1, 1, entries...)
	for i, number := range slots {
		items, err := danmakufile.Parse(entries[i].Files[0].Name, entries[i].Files[0].Data)
		if err != nil {
			e.t.Fatal(err)
		}
		episodeID := testenv.QueryInt(e.t, e.pool, `SELECT id FROM episodes WHERE season_id = 1 AND number = $1`, number)
		err = testenv.InTx(e.t, e.pool, func(tx pgx.Tx) error {
			return e.bindingSvc.CreateFileSlotInTx(e.t.Context(), tx, bindingsvc.FileSlot{
				SeasonBindingID: id, EpisodeID: episodeID, EntryID: entryIDs[i], Title: labels[i], Danmaku: items,
			})
		})
		if err != nil {
			e.t.Fatalf("CreateFileSlotInTx: %v", err)
		}
	}
	return id
}

// states 条目表："名称 状态 集号 原因"，没有的写作 -。
func states(d service.Detail) []string {
	got := make([]string, len(d.Items))
	for i, it := range d.Items {
		episode := "-"
		if it.EpisodeNumber != nil {
			episode = fmt.Sprint(*it.EpisodeNumber)
		}
		reason := ""
		if it.Reason != nil {
			reason = *it.Reason
		}
		if it.Replaces != nil {
			reason = "替换 " + it.Replaces.Title
		}
		got[i] = strings.TrimSpace(fmt.Sprintf("%s %s %s %s", it.Label, it.Status, episode, reason))
	}
	return got
}

// emptySlots 没有条目对到的槽位："集号 绑定标题"。
func emptySlots(d service.Detail) []string {
	got := make([]string, len(d.EmptySlots))
	for i, s := range d.EmptySlots {
		got[i] = fmt.Sprintf("%d %s", s.EpisodeNumber, s.Title)
	}
	return got
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
		if testenv.QueryInt(e.t, e.pool, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`) >= n {
			return
		}
	}
	e.t.Fatalf("等不到 %d 个连接在等锁", n)
}

// emptyIfNull null 读作空串。
func emptyIfNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
