package seasonbinding_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// TestBackfillSkipsBoundSource 集上已有同一个弹幕源的绑定、不是这个季绑定建出的：跳过，不拉取，条目为"已有同源绑定"；
// 那个绑定被删掉后，下一次补建补上。
func TestBackfillSkipsBoundSource(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
			Videos:      testenv.FakeVideos("a", "b"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		env.bindManually(2, "b")
		id := env.create().ID

		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b -"})
		testenv.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a"})
		testenv.AssertStrings(t, "条目", states(env.get(id)), []string{"a keep 1", "b alreadyBound 2"})

		env.exec(`DELETE FROM bindings WHERE season_binding_id IS NULL`)
		testenv.AssertStrings(t, "删掉手动绑定之后的条目", states(env.get(id)), []string{"a keep 1", "b create 2"})
		env.backfill(id)
		testenv.AssertStrings(t, "再补建之后的绑定", env.bindings(), []string{"1 a 1", "2 b 1"})
	})
}

// TestBackfillSourceBoundDuringFetch 补建停在拉取上时有人手动绑定了同一个弹幕源：不建绑定，条目为"已有同源绑定"。
func TestBackfillSourceBoundDuringFetch(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}}, Videos: testenv.FakeVideos("a")}
		env := newSeasonEnv(t, pool, src, 1)
		src.Gate()
		d := env.createAsync("list/s")
		<-src.Started
		env.bindManually(1, "a")
		src.Release <- struct{}{}
		synctest.Wait()

		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a -"})
		testenv.AssertStrings(t, "条目", states(env.get(d.ID)), []string{"a alreadyBound 1"})
	})
}

// TestBackfillSharedSource 两个季绑定对到同一集的同一个弹幕源：先建出的占着，后来的跳过；
// 先建出的那个季绑定被删除（连同它建出的绑定）之后，由后来的补上。
func TestBackfillSharedSource(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{
				"s": {Items: []source.CollectionItem{entry("a", 1)}},
				"t": {Items: []source.CollectionItem{entry("a", 1)}},
			},
			Videos: testenv.FakeVideos("a"),
		}
		env := newSeasonEnv(t, pool, src, 1)
		first := env.create().ID
		second := env.createAsync("list/t").ID
		synctest.Wait()

		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1"})
		testenv.AssertStrings(t, "后来的季绑定的条目", states(env.get(second)), []string{"a alreadyBound 1"})

		if err := env.svc.Delete(t.Context(), first); err != nil {
			t.Fatal(err)
		}
		env.backfill(second)

		testenv.AssertStrings(t, "删掉之后的绑定", env.bindings(), []string{"1 a 2"})
		testenv.AssertStrings(t, "后来的季绑定的条目", states(env.get(second)), []string{"a keep 1"})
	})
}

// TestBackfillRecreatedEpisode 删掉的集被同步用新 ID 建回来：算作新集，照常补建。
func TestBackfillRecreatedEpisode(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}},
			Videos:      testenv.FakeVideos("a"),
		}
		env := newSeasonEnv(t, pool, src, 1)
		id := env.create().ID
		env.exec(`DELETE FROM episodes WHERE id = 1`)
		testenv.AssertStrings(t, "删掉集之后的条目", states(env.get(id)), []string{"a noEpisode 1"})
		newID := env.addEpisode(1)

		env.backfill(id)

		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1"})
		if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM bindings WHERE episode_id = $1`, newID); n != 1 {
			t.Errorf("新的集上有 %d 个绑定，want 1", n)
		}
		testenv.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a", "a"})
	})
}

// TestBackfillNewItemsAndEpisodes 合集新增条目、目录新增集：下一轮只拉取新对上的；合集里去掉的条目，它的槽位留着。
func TestBackfillNewItemsAndEpisodes(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}}},
			Videos:      testenv.FakeVideos("a", "b", "c", "d"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		id := env.create().ID
		testenv.AssertStrings(t, "第一轮的拉取", src.FetchedNames(), []string{"a", "b"})

		src.Collections["s"] = source.Collection{Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3), entry("d", 4)}}
		env.addEpisode(3)
		env.backfill(id)

		testenv.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a", "b", "c"})
		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b 1", "3 c 1"})
		testenv.AssertStrings(t, "条目", states(env.get(id)), []string{"a keep 1", "b keep 2", "c keep 3", "d noEpisode 4"})

		src.Collections["s"] = source.Collection{Items: []source.CollectionItem{entry("b", 2), entry("c", 3)}}
		env.backfill(id)
		d := env.get(id)
		testenv.AssertStrings(t, "去掉条目之后", states(d), []string{"b keep 2", "c keep 3"})
		testenv.AssertStrings(t, "去掉条目之后的绑定", env.bindings(), []string{"1 a 1", "2 b 1", "3 c 1"})
		testenv.AssertStrings(t, "没有条目的槽位", emptySlots(d), []string{"1 弹幕源 a"})
	})
}

// TestBackfillItemError 单个条目上游错误：其他条目照建，失败原因记在条目上；下一轮再试，成功后清掉。
func TestBackfillItemError(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}}},
			Videos:      testenv.FakeVideos("a", "b", "c"),
			FetchErrs:   map[string]error{"b": &source.Error{Kind: source.Upstream, Message: "B 站接口异常", Err: errors.New("HTTP 503")}},
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		failedAt := time.Now()
		id := env.create().ID

		d := env.get(id)
		testenv.AssertStrings(t, "条目", states(d), []string{"a keep 1", "b create 2", "c keep 3"})
		if it := d.Items[1]; emptyIfNull(it.LastError) != "B 站接口异常" || it.LastErrorAt == nil || !it.LastErrorAt.Equal(failedAt) {
			t.Errorf("lastError = %v、lastErrorAt = %v, want B 站接口异常、%v", it.LastError, it.LastErrorAt, failedAt)
		}
		if d.LastError != nil {
			t.Errorf("lastError = %q, want 条目的失败不算这一轮的错误", *d.LastError)
		}

		delete(src.FetchErrs, "b")
		env.backfill(id)

		d = env.get(id)
		testenv.AssertStrings(t, "再补建一次之后的条目", states(d), []string{"a keep 1", "b keep 2", "c keep 3"})
		if d.Items[1].LastErrorAt != nil {
			t.Error("成功之后 lastErrorAt 应清掉")
		}
		testenv.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a", "b", "c", "b"})
	})
}

// TestBackfillRateLimited 限流：这一轮立即结束，后面的条目没有请求，上次检查时间照常更新、记下原因；下一轮接着做。
func TestBackfillRateLimited(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}}},
			Videos:      testenv.FakeVideos("a", "b", "c"),
			FetchErrs:   map[string]error{"b": &source.Error{Kind: source.RateLimited, Message: "B 站限流，请稍后再试"}},
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		start := time.Now()
		id := env.create().ID

		testenv.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a", "b"})
		d := env.get(id)
		if emptyIfNull(d.LastError) != "B 站限流，请稍后再试" || d.LastCheckedAt == nil || !d.LastCheckedAt.Equal(start) {
			t.Errorf("lastError = %v, lastCheckedAt = %v, want 限流的原因、这一轮的开始时间", d.LastError, d.LastCheckedAt)
		}
		testenv.AssertStrings(t, "条目", states(d), []string{"a keep 1", "b create 2", "c create 3"})

		delete(src.FetchErrs, "b")
		env.backfill(id)
		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b 1", "3 c 1"})
		if d := env.get(id); d.LastError != nil {
			t.Errorf("lastError = %q, want 成功后清掉", *d.LastError)
		}
	})
}

// TestBackfillCollectionGone 列出合集返回 NotFound：季绑定失效，建出的绑定不受影响；之后列出成功时恢复正常。
// 其他上游错误只记下原因，状态不变。
func TestBackfillCollectionGone(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}},
			Videos:      testenv.FakeVideos("a"),
		}
		env := newSeasonEnv(t, pool, src, 1)
		id := env.create().ID

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
			src.ListErr = step.listErr
			env.backfill(id)
			d := env.get(id)
			if d.Status != step.wantStatus || emptyIfNull(d.LastError) != step.wantError {
				t.Errorf("%s：状态 %s、错误 %q，want %s、%q", step.name, d.Status, emptyIfNull(d.LastError), step.wantStatus, step.wantError)
			}
			testenv.AssertStrings(t, step.name+"：绑定", env.bindings(), []string{"1 a 1"})
		}
	})
}

// TestBackfillWriteDuringDeleteSeason 删季锁住了第 1 集、还没删到季绑定时，补建开始写入第 1 集：
// 两边加锁的顺序不能相反（否则死锁），补建等删季做完，随后发现季绑定已被删除。
func TestBackfillWriteDuringDeleteSeason(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}}, Videos: testenv.FakeVideos("a")}
		env := newSeasonEnv(t, pool, src, 1, 2)
		src.Gate()
		env.createAsync("list/s")
		<-src.Started // a：还没开始写入

		// 第 2 集被另一个事务锁住，删季锁住季和第 1 集之后停在第 2 集上
		release := env.holdRowLock(`SELECT id FROM episodes WHERE number = 2 FOR UPDATE`)
		deleted := goDo(func() error { _, err := pool.Exec(t.Context(), `DELETE FROM seasons WHERE id = 1`); return err })
		env.waitLockWaiters(1)
		src.Release <- struct{}{}
		env.waitLockWaiters(2) // 补建的写入事务也在等
		release()

		if err := <-deleted; err != nil {
			t.Fatalf("删季: %v", err)
		}
		synctest.Wait()
		if logs := env.logs.String(); strings.Contains(logs, "level=ERROR") {
			t.Errorf("补建出错：\n%s", logs)
		}
		testenv.AssertStrings(t, "绑定", env.bindings(), nil)
	})
}

// TestBackfillTwice 同一个季绑定正在补建时再触发：409"正在补建"，不排队；详情的 running 为 true。
func TestBackfillTwice(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1)}}}, Videos: testenv.FakeVideos("a")}
		env := newSeasonEnv(t, pool, src, 1)
		src.Gate()
		d := env.createAsync("list/s")
		<-src.Started

		_, err := env.svc.Backfill(t.Context(), d.ID)
		testenv.AssertAppError(t, err, http.StatusConflict, "正在补建")
		if got := env.get(d.ID); !got.Running {
			t.Error("running = false, want 正在补建")
		}
		// 打开追更时正在补建：不另起一轮，也不报错
		env.setFollow(d.ID, true)

		src.Release <- struct{}{}
		synctest.Wait()
		if got := env.get(d.ID); got.Running {
			t.Error("补建结束后 running = true")
		}
		testenv.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a"})
		_, err = env.svc.Backfill(t.Context(), 9)
		testenv.AssertAppError(t, err, http.StatusNotFound, "季绑定不存在")
	})
}

// TestBackfillEpisodeDeletedDuringFetch 补建停在拉取上时删除目标集：这个条目被跳过，补建继续。
func TestBackfillEpisodeDeletedDuringFetch(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
			Videos:      testenv.FakeVideos("a", "b"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		src.Gate()
		d := env.createAsync("list/s")
		<-src.Started // a
		env.exec(`DELETE FROM episodes WHERE number = 1`)
		src.Release <- struct{}{}
		<-src.Started // b
		src.Release <- struct{}{}
		synctest.Wait()

		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"2 b 1"})
		testenv.AssertStrings(t, "条目", states(env.get(d.ID)), []string{"a noEpisode 1", "b keep 2"})
	})
}

// TestFollowChecksPeriodically 追更开着时按上次检查时间每满 12 小时检查一次（每分钟扫描）；关掉后不再检查。
func TestFollowChecksPeriodically(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {}}}
		env := newSeasonEnv(t, pool, src)
		id := env.create().ID
		checks := func() int { return src.ListCount() - 2 } // 创建时列出一次，随后补建又检查一次

		time.Sleep(testFollow.CheckInterval - testFollow.ScanInterval)
		synctest.Wait()
		if n := checks(); n != 0 {
			t.Fatalf("不到 12 小时又检查了 %d 次", n)
		}
		time.Sleep(2 * testFollow.ScanInterval)
		synctest.Wait()
		if n := checks(); n != 1 {
			t.Fatalf("满 12 小时后检查了 %d 次，want 1", n)
		}

		env.setFollow(id, false)
		time.Sleep(3 * testFollow.CheckInterval)
		synctest.Wait()
		if n := checks(); n != 1 {
			t.Errorf("关掉追更后又检查了 %d 次", n-1)
		}
	})
}

// TestFollowNewEpisode 目录同步进来新的集后一分钟内，追更开着的季绑定会去合集里找这一集；追更关着时不找。
func TestFollowNewEpisode(t *testing.T) {
	t.Parallel()
	for _, follow := range []bool{true, false} {
		name := "follow=true"
		if !follow {
			name = "follow=false"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				src := &testenv.FakeCollector{
					Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
					Videos:      testenv.FakeVideos("a", "b"),
				}
				env := newSeasonEnv(t, pool, src, 1)
				id := env.create().ID
				if !follow {
					env.setFollow(id, false)
				}
				time.Sleep(time.Hour)
				synctest.Wait()

				env.addEpisode(2)
				time.Sleep(testFollow.ScanInterval)
				synctest.Wait()

				want := []string{"1 a 1"}
				if follow {
					want = append(want, "2 b 1")
				}
				testenv.AssertStrings(t, "绑定", env.bindings(), want)
			})
		})
	}
}

// TestFollowRechecksDue 扫描列出到期的季绑定之后，排在后面的被手动补建检查过：轮到它时已不再到期，不重复检查。
func TestFollowRechecksDue(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {}, "t": {}}, Videos: testenv.FakeVideos("a")}
		env := newSeasonEnv(t, pool, src, 1)
		env.create()
		second := env.createAsync("list/t")
		synctest.Wait()
		// 合集 s 新出了一集：定期的检查里，第一个季绑定停在拉取上
		src.Collections["s"] = source.Collection{Items: []source.CollectionItem{entry("a", 1)}}
		src.Gate()

		time.Sleep(testFollow.CheckInterval)
		<-src.Started // 扫描列出了两个季绑定，第一个停在拉取 a 上
		lists := src.ListCount()
		env.backfill(second.ID)
		src.Release <- struct{}{}
		synctest.Wait()

		if n := src.ListCount() - lists; n != 1 {
			t.Errorf("第二个季绑定检查了 %d 次，want 只有手动补建的 1 次", n)
		}
		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1"})
	})
}

// TestFollowResumesAfterRestart 补建到一半时关闭服务：不写上次检查时间，重启后一分钟内从没做完的地方接着做。
func TestFollowResumesAfterRestart(t *testing.T) {
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
		<-src.Started // b

		env.stop() // 关闭服务：b 的拉取被取消
		if got := env.get(d.ID); got.LastCheckedAt != nil || got.Running {
			t.Errorf("关闭后 lastCheckedAt = %v、running = %v，want 不写、不在补建", got.LastCheckedAt, got.Running)
		}
		testenv.AssertStrings(t, "关闭时的绑定", env.bindings(), []string{"1 a 1"})

		src.Started = nil
		env.start()
		time.Sleep(testFollow.ScanInterval)
		synctest.Wait()

		testenv.AssertStrings(t, "重启后的绑定", env.bindings(), []string{"1 a 1", "2 b 1", "3 c 1"})
		testenv.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a", "b", "b", "c"})
	})
}

// TestManualBackfillConcurrent 不同季绑定的手动补建各自立即开始，互不等待。
func TestManualBackfillConcurrent(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{
				"s": {Items: []source.CollectionItem{entry("a", 1)}},
				"t": {Items: []source.CollectionItem{entry("b", 2)}},
			},
			Videos: testenv.FakeVideos("a", "b"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		src.Gate()
		env.createAsync("list/s")
		if name := <-src.Started; name != "a" {
			t.Fatalf("第一个季绑定拉取的是 %s", name)
		}

		second := env.createAsync("list/t")
		if !second.Running {
			t.Error("第二个季绑定 running = false, want 立即开始")
		}
		if name := <-src.Started; name != "b" {
			t.Fatalf("第一轮还停着时拉取的是 %s, want b", name)
		}
		src.Release <- struct{}{}
		src.Release <- struct{}{}
		synctest.Wait()

		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b 2"})
		testenv.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a", "b"})
	})
}

// TestFollowSkipsFolderSeasonBinding 追更的扫描不碰文件夹的季绑定：满了检查周期、季里有了新的集也不检查。
func TestFollowSkipsFolderSeasonBinding(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{}
		env := newSeasonEnv(t, pool, src, 1)
		env.folder([]string{"01"}, map[int]int{0: 1})

		time.Sleep(2 * testFollow.CheckInterval)
		env.addEpisode(2)
		time.Sleep(testFollow.ScanInterval)
		synctest.Wait()

		if n := src.ListCount(); n != 0 {
			t.Errorf("列出了 %d 次合集", n)
		}
		if logs := env.logs.String(); strings.Contains(logs, "level=ERROR") {
			t.Errorf("扫描出错：\n%s", logs)
		}
	})
}
