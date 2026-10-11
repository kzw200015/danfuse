package seasonbinding_test

import (
	"net/http"
	"reflect"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/seasonbinding"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// changeTo 换合集的参数：贴链接 link，只有一个候选。
func changeTo(link string) seasonbinding.UpdateParams {
	return seasonbinding.UpdateParams{Collection: &seasonbinding.CollectionChoice{Link: link}}
}

// TestChangeCollection 换合集：换成包含原来条目的合集时什么都不删，处理过的照旧，新条目随即补建；
// 换成另一部番时，它建出的、弹幕源不在新合集里的绑定连同处理过的记录删掉（用户已经删掉绑定的条目也清掉处理过的记录），
// 手动绑定不动；同一次请求里改的集号对应对新合集生效。编辑时的预览先给出这些后果。
func TestChangeCollection(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{
				"p": {Title: "多 P", Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}},
				"s": {Title: "合集", Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}},
				"o": {Title: "别的番", Items: []source.CollectionItem{entry("x", 1), entry("y", 2)}},
			},
			Videos: testenv.FakeVideos("a", "b", "c", "x", "y"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		env.bindManually(1, "m")
		id := env.createAsync("list/p").ID
		synctest.Wait()
		testenv.AssertStrings(t, "换之前的绑定", env.bindings(), []string{"1 m -", "1 a 1", "2 b 1"})

		preview, err := env.svc.PreviewChange(t.Context(), id, "list/p", source.DefaultEpisodeRule())
		if err != nil || len(preview.Candidates) != 1 || !preview.Candidates[0].Current {
			t.Errorf("PreviewChange(现在的合集) = (%+v, %v), want 标出是现在的合集", preview, err)
		}
		preview, err = env.svc.PreviewChange(t.Context(), id, "list/s", source.DefaultEpisodeRule())
		if err != nil {
			t.Fatalf("PreviewChange: %v", err)
		}
		want := seasonbinding.ChangePreview{Candidates: []seasonbinding.ChangeCandidate{{
			Kind: "list", Title: "合集", SourceURL: "https://fake.test/list/s", SourceLabel: "假合集 s",
			Items: []seasonbinding.ChangeItem{
				{Label: "a", Number: new(1), HandledEpisode: new(int32(1))},
				{Label: "b", Number: new(2), HandledEpisode: new(int32(2))},
				{Label: "c", Number: new(3)},
			},
		}}}
		if !reflect.DeepEqual(preview, want) {
			t.Errorf("PreviewChange() = %+v\nwant %+v", preview, want)
		}

		// 换成包含原来条目的合集
		got, err := env.svc.Update(t.Context(), id, changeTo("list/s"))
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if *got.Link != "list/s" || got.Title != "合集" || *got.SourceURL != "https://fake.test/list/s" || got.LastCheckedAt != nil || !got.Running {
			t.Errorf("Update() = %+v, want 新合集、新链接、还没检查过、正在补建", got.View)
		}
		synctest.Wait()
		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 m -", "1 a 1", "2 b 1", "3 c 1"})
		testenv.AssertStrings(t, "处理过", env.handled(), []string{"1 a", "1 b", "1 c"})
		testenv.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a", "b", "c"})

		// 用户删掉了 c 的绑定，再换成另一部番，同时改集号对应
		env.exec(`DELETE FROM bindings WHERE ref->>'name' = 'c'`)
		preview, err = env.svc.PreviewChange(t.Context(), id, "list/o", source.DefaultEpisodeRule())
		if c := preview.Candidates[0]; err != nil || c.Current || c.RemovedBindings != 2 || c.Items[0].HandledEpisode != nil {
			t.Errorf("PreviewChange(另一部番) = (%+v, %v), want 会删掉 2 个绑定、没有处理过的条目", preview, err)
		}
		p := changeTo("list/o")
		p.MappingTo = new(int32(2))
		if _, err := env.svc.Update(t.Context(), id, p); err != nil {
			t.Fatalf("Update: %v", err)
		}
		synctest.Wait()
		testenv.AssertStrings(t, "换成另一部番之后的绑定", env.bindings(), []string{"1 m -", "2 x 1", "3 y 1"})
		testenv.AssertStrings(t, "换成另一部番之后的处理过", env.handled(), []string{"1 x", "1 y"})
		testenv.AssertStrings(t, "换成另一部番之后的条目", states(env.get(id)), []string{"x bound 2", "y bound 3"})
	})
}

// TestChangeCollectionSameCollection 换成的就是现在的合集（同一个合集的另一种链接）：只改记下的链接，
// 上次检查时间保留；即使合集里已经没有某个条目，它建出的绑定也不删。记下链接之前建的季绑定输出合集的链接。
func TestChangeCollectionSameCollection(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
			Videos:      testenv.FakeVideos("a", "b"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		before := env.create(1, 1)
		synctest.Wait()
		checked := env.get(before.ID).LastCheckedAt
		env.exec(`UPDATE season_bindings SET link = NULL`)
		if got := env.get(before.ID); *got.Link != *got.SourceURL {
			t.Errorf("记下链接之前建的季绑定 link = %s, want 合集的链接 %s", *got.Link, *got.SourceURL)
		}
		src.Collections["s"] = source.Collection{Items: []source.CollectionItem{entry("a", 1)}} // UP 主删掉了 b

		preview, err := env.svc.PreviewChange(t.Context(), before.ID, "alias-list/s", source.DefaultEpisodeRule())
		if c := preview.Candidates[0]; err != nil || !c.Current || c.RemovedBindings != 0 {
			t.Errorf("PreviewChange() = (%+v, %v), want 现在的合集、不删绑定", preview, err)
		}
		got, err := env.svc.Update(t.Context(), before.ID, changeTo("alias-list/s"))
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if *got.Link != "alias-list/s" || got.LastCheckedAt == nil || !got.LastCheckedAt.Equal(*checked) {
			t.Errorf("Update() = %+v, want 新链接、上次检查时间 %v", got.View, checked)
		}
		synctest.Wait()
		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b 1"})
		testenv.AssertStrings(t, "条目", states(env.get(before.ID)), []string{"a bound 1"})
	})
}

// TestChangeCollectionDuringBackfill 正在补建时换合集返回 409，什么都不改；补建照常做完。
func TestChangeCollectionDuringBackfill(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{
				"p": {Items: []source.CollectionItem{entry("a", 1)}},
				"o": {Items: []source.CollectionItem{entry("x", 1)}},
			},
			Videos: testenv.FakeVideos("a", "x"),
		}
		env := newSeasonEnv(t, pool, src, 1)
		src.Gate()
		d := env.createAsync("list/p")
		<-src.Started

		_, err := env.svc.Update(t.Context(), d.ID, changeTo("list/o"))
		testenv.AssertAppError(t, err, http.StatusConflict, "正在补建，补建结束后再换合集")

		src.Release <- struct{}{}
		synctest.Wait()
		if got := env.get(d.ID); *got.Link != "list/p" {
			t.Errorf("被拒绝之后 link = %s, want 不变", *got.Link)
		}
		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1"})
	})
}

// TestChangeCollectionDuplicate 换成这一季另一个季绑定的合集：409，不列出合集，什么都不改。
func TestChangeCollectionDuplicate(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"p": {}, "s": {}}}
		env := newSeasonEnv(t, pool, src, 1)
		p := env.createAsync("list/p")
		env.createAsync("list/s")
		synctest.Wait()
		lists := src.ListCount()

		_, err := env.svc.Update(t.Context(), p.ID, changeTo("alias-list/s"))
		testenv.AssertAppError(t, err, http.StatusConflict, "这一季已经绑定过这个合集")
		if n := src.ListCount(); n != lists {
			t.Errorf("列出了 %d 次合集，want 不列出", n-lists)
		}
		if got := env.get(p.ID); *got.Link != "list/p" {
			t.Errorf("被拒绝之后 link = %s, want 不变", *got.Link)
		}
	})
}
