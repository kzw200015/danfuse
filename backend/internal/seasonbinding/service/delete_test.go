package service_test

import (
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5/pgxpool"

	bindingsvc "github.com/kzw200015/danfuse/backend/internal/binding/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// TestDeleteSeasonBinding 删除季绑定：它建出的绑定（连同弹幕）、合集的条目表、文件夹的条目和条目的文件一起删掉；
// 手动建的绑定、别的季绑定建出的绑定不受影响。
func TestDeleteSeasonBinding(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
			Videos:      testenv.FakeVideos("a", "b"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		env.bindManually(1, "m")
		if _, err := env.bindingSvc.CreateFromFiles(t.Context(), 1, []bindingsvc.UploadedFile{testenv.Snapshot1("手动上传.xml")}); err != nil {
			t.Fatal(err)
		}
		collection := env.create().ID
		folder := env.folder([]string{"01", "02"}, map[int]int{0: 1, 1: 2})

		if err := env.svc.Delete(t.Context(), folder); err != nil {
			t.Fatalf("Delete(folder): %v", err)
		}
		testenv.AssertStrings(t, "删掉文件夹的季绑定之后的绑定", env.bindings(), []string{"1 m -", "1 手动上传 -", "1 a 1", "2 b 1"})
		for _, table := range []string{"season_binding_entries", "season_binding_entry_files"} {
			if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM `+table); n != 0 {
				t.Errorf("%s 还有 %d 行", table, n)
			}
		}

		if err := env.svc.Delete(t.Context(), collection); err != nil {
			t.Fatalf("Delete(collection): %v", err)
		}
		testenv.AssertStrings(t, "删掉合集的季绑定之后的绑定", env.bindings(), []string{"1 m -", "1 手动上传 -"})
		for _, table := range []string{"season_bindings", "season_binding_items"} {
			if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM `+table); n != 0 {
				t.Errorf("%s 还有 %d 行", table, n)
			}
		}
		if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM danmaku d JOIN bindings b ON b.id = d.binding_id`); n != testenv.QueryInt(t, pool, `SELECT count(*) FROM danmaku`) {
			t.Error("留下了不属于任何绑定的弹幕")
		}
		testenv.AssertAppError(t, env.svc.Delete(t.Context(), collection), http.StatusNotFound, "季绑定不存在")
	})
}

// TestDeleteSeasonBindingDuringBackfill 补建停在拉取上时删除季绑定：补建随即结束，之前建出的绑定一起删掉，正在建的不会漏下。
func TestDeleteSeasonBindingDuringBackfill(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}}},
			Videos:      testenv.FakeVideos("a", "b", "c"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		src.Gate()
		d := env.createAsync("list/s")
		<-src.Started // a
		src.Release <- struct{}{}
		<-src.Started // b：a 已经写入

		if err := env.svc.Delete(t.Context(), d.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		src.Release <- struct{}{}
		synctest.Wait()

		testenv.AssertStrings(t, "绑定", env.bindings(), nil)
		testenv.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a", "b"})
	})
}

// TestDeleteSeasonBindingQueuesBehindDeleteSeason 删季锁住了季和第 1 集、还没删到季绑定时删除季绑定：
// 删除季绑定的第一句先锁季，排在删季之后，删季做完后季绑定已不存在。
func TestDeleteSeasonBindingQueuesBehindDeleteSeason(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}}, Videos: testenv.FakeVideos("a")}
		env := newSeasonEnv(t, pool, src, 1, 2)
		id := env.create().ID

		// 第 2 集被另一个事务锁住，删季锁住季和第 1 集之后停在第 2 集上
		release := env.holdRowLock(`SELECT id FROM episodes WHERE number = 2 FOR UPDATE`)
		deletedSeason := goDo(func() error { _, err := pool.Exec(t.Context(), `DELETE FROM seasons WHERE id = 1`); return err })
		env.waitLockWaiters(1)
		deletedBinding := goDo(func() error { return env.svc.Delete(t.Context(), id) })
		env.waitLockWaiters(2)
		release()

		if err := <-deletedSeason; err != nil {
			t.Fatalf("删季: %v", err)
		}
		testenv.AssertAppError(t, <-deletedBinding, http.StatusNotFound, "季绑定不存在")
		testenv.AssertStrings(t, "绑定", env.bindings(), nil)
	})
}

// TestDeleteSeasonBindingDuringDeleteSeason 删季停在第 2 集上手动建的绑定上时（季、集、季绑定都已锁住），删除季绑定：
// 删除季绑定在锁季那一句就排队，不死锁；删季做完后季绑定已不存在。
func TestDeleteSeasonBindingDuringDeleteSeason(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}}, Videos: testenv.FakeVideos("a")}
		env := newSeasonEnv(t, pool, src, 1, 2)
		env.exec(`INSERT INTO bindings (episode_id, adapter, ref, title, duration) VALUES (2, 'fake', '{"name": "m"}', '手动', 1420)`)
		id := env.create().ID

		// 第 2 集上手动建的绑定被另一个事务锁住，删季（季 → 集 → 季绑定 → 绑定）停在它上面
		release := env.holdRowLock(`SELECT id FROM bindings WHERE ref->>'name' = 'm' FOR UPDATE`)
		deletedSeason := goDo(func() error { _, err := pool.Exec(t.Context(), `DELETE FROM seasons WHERE id = 1`); return err })
		env.waitLockWaiters(1)
		deletedBinding := goDo(func() error { return env.svc.Delete(t.Context(), id) })
		env.waitLockWaiters(2)
		release()

		if err := <-deletedSeason; err != nil {
			t.Fatalf("删季: %v", err)
		}
		testenv.AssertAppError(t, <-deletedBinding, http.StatusNotFound, "季绑定不存在")
		testenv.AssertStrings(t, "绑定", env.bindings(), nil)
	})
}
