package service_test

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/danmakufile"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// TestCollectionDetail 合集的季绑定的详情：季绑定的 JSON（合集的字段），各条目按上次列出的结果和现在的表单派生：
// 在起点之前、已建绑定（保留）、集上已有同源绑定、对不上（平台没给出整数集号、集号重复）、目录里没有这一集。
func TestCollectionDetail(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Title: "某番剧", Finished: true, Items: []source.CollectionItem{
				entry("z", 0), entry("a", 1), entry("b", 2), entry("c", 3), odd("sp", "集号「SP」不是整数"),
				entry("d1", 4), entry("d2", 4), entry("e", 5),
			}}},
			Videos: testenv.FakeVideos("z", "a", "b", "c", "e", "other"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3, 4)
		env.bindManually(2, "b")
		env.bindManually(3, "other")
		start := time.Now()

		created := env.create()

		if created.CollectionView == nil || !created.Running || !created.Follow || created.FolderView != nil {
			t.Errorf("创建时的详情 = %+v, want 合集的季绑定、正在补建、追更开着", created.View)
		}
		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b -", "3 other -", "3 c 1"})

		got := env.get(created.ID)
		want := service.View{
			ID: 1, SeasonID: 1, Kind: "collection", Title: "某番剧",
			EpisodePatterns: source.DefaultEpisodeRule().Patterns(), MappingFrom: 1, MappingTo: 1, BindingCount: 2,
			CollectionView: &service.CollectionView{
				Adapter: "fake", Link: "list/s", SourceURL: "https://fake.test/list/s", SourceLabel: "假合集 s",
				Follow: true, Status: "active", LastCheckedAt: &start,
			},
		}
		if got.LastCheckedAt == nil || !got.LastCheckedAt.Equal(start) {
			t.Errorf("lastCheckedAt = %v, want 这一轮的开始时间 %v", got.LastCheckedAt, start)
		}
		got.LastCheckedAt = &start
		want.CreatedAt = got.CreatedAt // 由数据库写入，不是气泡里的假时间
		if !reflect.DeepEqual(got.View, want) {
			t.Errorf("Get() = %+v %+v\nwant %+v %+v", got.View, got.CollectionView, want, want.CollectionView)
		}
		testenv.AssertStrings(t, "条目", states(got), []string{
			"z beforeStart -", "a keep 1", "b alreadyBound 2", "c keep 3", "sp unmatched - 平台没有给出整数集号",
			"d1 unmatched - 集号重复", "d2 unmatched - 集号重复", "e noEpisode 5",
		})
		testenv.AssertStrings(t, "没有条目的槽位", emptySlots(got), nil)
	})
}

// TestDetailEmptySlotAndReplace 条目从合集里去掉：补建不删它的槽位，详情列在 emptySlots 里；
// 槽位所在的集对到了别的弹幕源而替换失败：原来的绑定保留，条目显示要替换，带着失败原因。
func TestDetailEmptySlotAndReplace(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
			Videos:      testenv.FakeVideos("a", "b"),
			FetchErrs:   map[string]error{"b2": &source.Error{Kind: source.Upstream, Message: "B 站接口异常", Err: errors.New("HTTP 503")}},
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		id := env.create().ID

		src.Collections["s"] = source.Collection{Items: []source.CollectionItem{entry("b2", 2)}}
		env.backfill(id)

		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b 1"})
		d := env.get(id)
		testenv.AssertStrings(t, "条目", states(d), []string{"b2 replace 2 替换 弹幕源 b"})
		testenv.AssertStrings(t, "没有条目的槽位", emptySlots(d), []string{"1 弹幕源 a"})
		if it := d.Items[0]; emptyIfNull(it.LastError) != "B 站接口异常" || it.LastErrorAt == nil {
			t.Errorf("条目的失败原因 = %v、%v", it.LastError, it.LastErrorAt)
		}
	})
}

// TestFolderDetail 文件夹的季绑定的详情：条目名称现拼为"季绑定标题 / 条目名称"再认集号，槽位里是这个条目的为保留，
// 没有槽位的为新建，认不出的对不上；失败原因恒为 null。
func TestFolderDetail(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		env := newSeasonEnv(t, pool, &testenv.FakeCollector{}, 1, 2)
		id := env.folder([]string{"01", "02", "花絮"}, map[int]int{0: 1})

		d := env.get(id)
		if d.Kind != "folder" || d.FolderView == nil || d.EntryCount != 3 || d.BindingCount != 1 || d.CollectionView != nil {
			t.Errorf("Get() = %+v, want 文件夹的季绑定、3 个条目、1 个绑定", d.View)
		}
		testenv.AssertStrings(t, "条目", states(d), []string{
			"星海旅人 / 01 keep 1", "星海旅人 / 02 create 2", "星海旅人 / 花絮 unmatched - 不符合集号规则",
		})
		testenv.AssertJSON(t, d.Items[0], `{
			"label": "星海旅人 / 01", "number": 1, "episodeNumber": 1, "status": "keep", "reason": null, "replaces": null,
			"lastError": null, "lastErrorAt": null
		}`)
		testenv.AssertJSON(t, d.EmptySlots, `[]`)
	})
}

// TestListBySeries 剧详情里的季绑定：两种按创建顺序排在一起，各自只带自己那一种的字段。
func TestListBySeries(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Title: "某番剧", Items: []source.CollectionItem{entry("a", 1)}}},
			Videos:      testenv.FakeVideos("a"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		folder := env.folder([]string{"01", "02"}, map[int]int{0: 1, 1: 2})
		env.create()

		views, err := env.svc.ListBySeries(t.Context(), 1)
		if err != nil {
			t.Fatalf("ListBySeries: %v", err)
		}
		if len(views[1]) != 2 || views[1][0].ID != folder {
			t.Fatalf("第 1 季的季绑定 = %+v", views[1])
		}
		common := []string{"bindingCount", "createdAt", "episodePatterns", "id", "kind", "mappingFrom", "mappingTo", "seasonId", "title"}
		for i, want := range []struct {
			kind, title string
			bindings    int32
			fields      []string
		}{
			{"folder", "星海旅人", 2, []string{"entryCount"}},
			{"collection", "某番剧", 1, []string{
				"adapter", "follow", "lastCheckedAt", "lastError", "link", "numberedByRule", "running", "sourceLabel", "sourceUrl", "status",
			}},
		} {
			v := views[1][i]
			if v.Kind != want.kind || v.Title != want.title || v.BindingCount != want.bindings {
				t.Errorf("第 %d 个季绑定 = %+v", i+1, v)
			}
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]json.RawMessage
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
			testenv.AssertStrings(t, want.kind+"的字段", slices.Sorted(maps.Keys(m)), slices.Sorted(slices.Values(append(want.fields, common...))))
		}
		if views[1][0].EntryCount != 2 {
			t.Errorf("entryCount = %d, want 2", views[1][0].EntryCount)
		}
	})
}

// TestSetFollow 开关追更只用于合集的季绑定：文件夹的为 400，不存在为 404；关掉不补建，打开时随即补建一次。
func TestSetFollow(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {}}}
		env := newSeasonEnv(t, pool, src, 1)
		id := env.create().ID
		folder := env.folder([]string{"01"}, nil)
		lists := src.ListCount()

		env.setFollow(id, false)
		synctest.Wait()
		if d := env.get(id); d.Follow || src.ListCount() != lists {
			t.Errorf("关掉追更之后 follow = %v、列出了 %d 次", d.Follow, src.ListCount()-lists)
		}
		env.setFollow(id, true)
		synctest.Wait()
		if d := env.get(id); !d.Follow || src.ListCount() != lists+1 {
			t.Errorf("打开追更之后 follow = %v、列出了 %d 次，want 补建一次", d.Follow, src.ListCount()-lists)
		}

		_, err := env.svc.SetFollow(t.Context(), folder, true)
		testenv.AssertAppError(t, err, http.StatusBadRequest, "这不是合集的季绑定")
		_, err = env.svc.SetFollow(t.Context(), 9, true)
		testenv.AssertAppError(t, err, http.StatusNotFound, "季绑定不存在")
	})
}

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
		if _, err := env.bindingSvc.CreateFromFiles(t.Context(), 1, []danmakufile.File{testenv.Snapshot1("手动上传.xml")}); err != nil {
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
