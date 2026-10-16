package service_test

import (
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/infra/testutil"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// TestCreateCollection 创建后在后台补建：对得上、目录里有的集建出绑定，季绑定 ID 指向它；
// 集上已有同一个弹幕源的手动绑定时跳过、不拉取；集上有别的绑定时照建。
func TestCreateCollection(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Title: "某番剧", Items: []source.CollectionItem{
				entry("z", 0), entry("a", 1), entry("b", 2), entry("c", 3), entry("e", 5),
			}}},
			Videos: testenv.FakeVideos("z", "a", "b", "c", "e", "other"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3, 4)
		env.bindManually(2, "b")
		env.bindManually(3, "other")

		env.create()

		testutil.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b -", "3 other -", "3 c 1"})
		testutil.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a", "c"})
	})
}

func TestCreateCollectionFailed(t *testing.T) {
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
			testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {Title: "合集"}}, ListErr: tt.listErr}
				env := newSeasonEnv(t, pool, src, 1)

				f := collectionForm(tt.link)
				f.CollectionKind = tt.kind
				_, err := env.svc.CreateCollection(t.Context(), tt.seasonID, f)

				testutil.AssertAppError(t, err, tt.wantStatus, tt.wantMessage)
				if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM season_bindings`); n != 0 {
					t.Errorf("写入了 %d 个季绑定", n)
				}
			})
		})
	}
}

// TestCreateCollectionDuplicate 同一季重复绑定同一个合集（不论链接怎么写）为 409，不再列出合集；
// 一季可以有多个季绑定，同一个合集也可以绑到别的季上。按种类选候选。
func TestCreateCollectionDuplicate(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {Title: "合集 s"}, "t": {Title: "合集 t"}}}
		env := newSeasonEnv(t, pool, src)
		env.create()
		lists := src.ListCount()

		_, err := env.svc.CreateCollection(t.Context(), 1, collectionForm("alias-list/s"))
		testutil.AssertAppError(t, err, http.StatusConflict, "这一季已经绑定过这个合集")
		if n := src.ListCount(); n != lists {
			t.Errorf("又列出了 %d 次合集，want 0：重复的不列出", n-lists)
		}

		both := collectionForm("both/t")
		both.CollectionKind = "list"
		if _, err := env.svc.CreateCollection(t.Context(), 1, both); err != nil {
			t.Errorf("同一季绑定另一个合集：%v", err)
		}
		if _, err := env.svc.CreateCollection(t.Context(), 2, collectionForm("list/s")); err != nil {
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
		testutil.AssertStrings(t, "季绑定", got, want)
	})
}

// TestCreateCollectionSeasonDeleted 列出合集期间这一季被删除：404"这一季已被删除"，什么都不写。
func TestCreateCollectionSeasonDeleted(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {Title: "合集"}}}
		env := newSeasonEnv(t, pool, src, 1)
		src.ListStarted, src.ListRelease = make(chan struct{}), make(chan struct{})

		errc := make(chan error, 1)
		go func() {
			_, err := env.svc.CreateCollection(t.Context(), 1, collectionForm("list/s"))
			errc <- err
		}()
		<-src.ListStarted
		env.exec(`DELETE FROM seasons WHERE id = 1`)
		close(src.ListRelease)

		testutil.AssertAppError(t, <-errc, http.StatusNotFound, "这一季已被删除")
		if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM season_bindings`); n != 0 {
			t.Errorf("写入了 %d 个季绑定", n)
		}
	})
}

// candidateStates 预览的一组里的条目，写法同 states。
func candidateStates(c service.Candidate) []string {
	return states(service.Detail{Items: c.Items})
}

// TestPreviewNewCollection 新建的预览：每个候选各一组，按请求里的集号规则、集号对应派生；集上已有同一个弹幕源的手动绑定为"已有同源绑定"。
// 不写库，季不存在为 404。
func TestPreviewNewCollection(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {Title: "某番剧", Finished: true, Items: []source.CollectionItem{
			entry("z", 0), entry("a", 1), entry("b", 2), odd("sp", "集号「SP」不是整数"), entry("e", 5),
		}}}}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		env.bindManually(2, "b")

		p, err := env.svc.PreviewNewCollection(t.Context(), 1, collectionForm("both/s"))
		if err != nil {
			t.Fatal(err)
		}

		if len(p.Candidates) != 2 {
			t.Fatalf("候选 %d 个，want 2", len(p.Candidates))
		}
		for i, want := range []struct{ kind, label string }{{"pages", "假多 P s"}, {"list", "假合集 s"}} {
			c := p.Candidates[i]
			if *c.CollectionKind != want.kind || *c.SourceLabel != want.label || c.Title != "某番剧" || !c.Finished || c.Current {
				t.Errorf("候选 %d = %s %s %q finished=%v current=%v", i, *c.CollectionKind, *c.SourceLabel, c.Title, c.Finished, c.Current)
			}
			testutil.AssertStrings(t, "条目", candidateStates(c), []string{
				"z beforeStart -", "a create 1", "b alreadyBound 2", "sp unmatched - 集号「SP」不是整数", "e noEpisode 5",
			})
			if want := (service.Summary{Create: 1}); c.Summary != want {
				t.Errorf("汇总 = %+v, want %+v", c.Summary, want)
			}
		}
		if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM season_bindings`); n != 0 {
			t.Errorf("写入了 %d 个季绑定", n)
		}

		_, err = env.svc.PreviewNewCollection(t.Context(), 9, collectionForm("list/s"))
		testutil.AssertAppError(t, err, http.StatusNotFound, "季不存在")
	})
}

// removedSlots 预览的一组里保存后删除的槽位，写法同 emptySlots。
func removedSlots(c service.Candidate) []string {
	return emptySlots(service.Detail{EmptySlots: c.Removed})
}

// TestPreviewCollectionEdit 编辑的预览：按这次列出的结果与季绑定的槽位比较，标出保存的候选；
// 槽位里是别的弹幕源的为替换，没有条目对到的槽位列为保存后删除。不写库。季绑定不存在为 404，文件夹的为 400。
func TestPreviewCollectionEdit(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Title: "某番剧", Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}}},
			Videos:      testenv.FakeVideos("a", "b", "c", "x"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		id := env.create().ID
		folder := env.folder([]string{"01"}, nil)
		src.Collections["s"] = source.Collection{Title: "某番剧", Items: []source.CollectionItem{entry("a", 1), entry("x", 2)}}

		p, err := env.svc.PreviewCollectionEdit(t.Context(), id, collectionForm("both/s"))
		if err != nil {
			t.Fatal(err)
		}

		if len(p.Candidates) != 2 || p.Candidates[0].Current || !p.Candidates[1].Current {
			t.Fatalf("候选 = %+v，want 第二个（list）是保存的", p.Candidates)
		}
		c := p.Candidates[1]
		testutil.AssertStrings(t, "条目", candidateStates(c), []string{"a keep 1", "x replace 2 替换 弹幕源 b"})
		testutil.AssertStrings(t, "保存后删除", removedSlots(c), []string{"3 弹幕源 c"})
		if want := (service.Summary{Replace: 1, Remove: 1, Keep: 1}); c.Summary != want {
			t.Errorf("汇总 = %+v, want %+v", c.Summary, want)
		}
		testutil.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b 1", "3 c 1"})

		_, err = env.svc.PreviewCollectionEdit(t.Context(), 99, collectionForm("list/s"))
		testutil.AssertAppError(t, err, http.StatusNotFound, "季绑定不存在")
		_, err = env.svc.PreviewCollectionEdit(t.Context(), folder, collectionForm("list/s"))
		testutil.AssertAppError(t, err, http.StatusBadRequest, "这不是合集的季绑定")
	})
}

// bindingIDs 季绑定 id 建出的绑定："集号 绑定 ID"，按集号排序。
func (e *seasonEnv) bindingIDs(id int64) []string {
	e.t.Helper()
	rows, err := e.pool.Query(e.t.Context(), `
		SELECT format('%s %s', e.number, b.id)
		FROM bindings b
		JOIN episodes e ON e.id = b.episode_id
		WHERE b.season_binding_id = $1
		ORDER BY e.number`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		e.t.Fatal(err)
	}
	return got
}

// TestSaveCollection 保存表单：以这次列出的结果为准（不和预览核对），事务里删掉没有条目的槽位，保留的绑定不动；
// 替换和新建交给随后的后台补建。没改表单也照样保存。
func TestSaveCollection(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Title: "某番剧", Items: []source.CollectionItem{entry("a", 1), entry("b", 2), entry("c", 3)}}},
			Videos:      testenv.FakeVideos("a", "b", "c", "x"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		id := env.create().ID
		before := env.bindingIDs(id)
		p, err := env.svc.PreviewCollectionEdit(t.Context(), id, collectionForm("list/s"))
		if err != nil {
			t.Fatal(err)
		}
		testutil.AssertStrings(t, "预览里保存后删除的", removedSlots(p.Candidates[0]), nil)

		// 预览之后合集又变了：保存以这次列出的为准
		src.Collections["s"] = source.Collection{Title: "某番剧（新）", Items: []source.CollectionItem{entry("a", 1), entry("x", 2)}}
		src.Gate()
		d, err := env.svc.SaveCollection(t.Context(), id, collectionForm("list/s"))
		if err != nil {
			t.Fatal(err)
		}

		if d.Title != "某番剧（新）" {
			t.Errorf("title = %q", d.Title)
		}
		testutil.AssertStrings(t, "保存之后、补建之前的绑定", env.bindings(), []string{"1 a 1", "2 b 1"})
		testutil.AssertStrings(t, "保存返回的条目", states(d), []string{"a keep 1", "x replace 2 替换 弹幕源 b"})
		testutil.AssertStrings(t, "没有条目的槽位", emptySlots(d), nil)
		<-src.Started
		src.Release <- struct{}{}
		synctest.Wait()

		testutil.AssertStrings(t, "补建之后的绑定", env.bindings(), []string{"1 a 1", "2 x 1"})
		if got := env.bindingIDs(id); got[0] != before[0] {
			t.Errorf("保留的绑定 = %s, want %s：不动", got[0], before[0])
		}
	})
}

// TestSaveCollectionSwitch 换成另一个合集：条目表换成新合集的，上次检查时间清掉，弹幕源相同的槽位保留，
// 其余没有条目的槽位删掉；改过的集号对应、集号规则一起保存。
func TestSaveCollectionSwitch(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{
				"s": {Title: "合集 s", Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}},
				"t": {Title: "合集 t", Items: []source.CollectionItem{entry("b", 1), entry("y", 2)}},
			},
			Videos: testenv.FakeVideos("a", "b", "y"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2, 3)
		id := env.create().ID
		before := env.bindingIDs(id)

		f := collectionForm("both/t")
		f.CollectionKind = "list"
		f.Mapping = source.Mapping{From: 1, To: 2}
		rule, err := source.ParseEpisodeRule([]string{`(\d+)`})
		if err != nil {
			t.Fatal(err)
		}
		f.Rule = rule
		src.Gate()
		d, err := env.svc.SaveCollection(t.Context(), id, f)
		if err != nil {
			t.Fatal(err)
		}

		if d.Link != "both/t" || d.Title != "合集 t" || d.SourceLabel != "假合集 t" || d.LastCheckedAt != nil ||
			d.MappingFrom != 1 || d.MappingTo != 2 || len(d.EpisodePatterns) != 1 {
			t.Errorf("保存之后 = %+v %+v", d.View, d.CollectionView)
		}
		testutil.AssertStrings(t, "条目", states(d), []string{"b keep 2", "y create 3"})
		testutil.AssertStrings(t, "保存之后的绑定", env.bindings(), []string{"2 b 1"})
		if got := env.bindingIDs(id); got[0] != before[1] {
			t.Errorf("弹幕源相同的槽位 = %s, want %s", got[0], before[1])
		}
		<-src.Started
		src.Release <- struct{}{}
		synctest.Wait()

		testutil.AssertStrings(t, "补建之后的绑定", env.bindings(), []string{"2 b 1", "3 y 1"})
		if d := env.get(id); d.LastCheckedAt == nil {
			t.Error("补建之后应写上次检查时间")
		}
	})
}

// TestSaveCollectionFailed 补建中保存为 409，什么都不改；同一季已经绑定过这个合集为 409（保存它自己的合集不算）；
// 不存在为 404，文件夹的季绑定为 400。
func TestSaveCollectionFailed(t *testing.T) {
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
		s := env.create().ID
		folder := env.folder([]string{"01"}, nil)

		src.Gate()
		tID := env.createAsync("list/t").ID
		<-src.Started
		_, err := env.svc.SaveCollection(t.Context(), tID, collectionForm("list/s"))
		testutil.AssertAppError(t, err, http.StatusConflict, "正在补建或保存，稍后再试")
		src.Release <- struct{}{}
		synctest.Wait()
		src.Started = nil

		_, err = env.svc.SaveCollection(t.Context(), tID, collectionForm("alias-list/s"))
		testutil.AssertAppError(t, err, http.StatusConflict, "这一季已经绑定过这个合集")
		if d := env.get(tID); d.Link != "list/t" {
			t.Errorf("link = %q, want 不变", d.Link)
		}
		if _, err := env.svc.SaveCollection(t.Context(), s, collectionForm("alias-list/s")); err != nil {
			t.Errorf("保存它自己的合集：%v", err)
		}
		synctest.Wait()

		_, err = env.svc.SaveCollection(t.Context(), 99, collectionForm("list/s"))
		testutil.AssertAppError(t, err, http.StatusNotFound, "季绑定不存在")
		_, err = env.svc.SaveCollection(t.Context(), folder, collectionForm("list/s"))
		testutil.AssertAppError(t, err, http.StatusBadRequest, "这不是合集的季绑定")
	})
}

// TestSetFollowKeepsBindings 开关追更不删绑定：合集里去掉的条目，它的槽位在关掉、打开追更（随即补建）之后都留着。
func TestSetFollowKeepsBindings(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
			Videos:      testenv.FakeVideos("a", "b"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		id := env.create().ID
		src.Collections["s"] = source.Collection{Items: []source.CollectionItem{entry("a", 1)}}

		env.setFollow(id, false)
		env.setFollow(id, true)
		synctest.Wait()

		testutil.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b 1"})
		testutil.AssertStrings(t, "没有条目的槽位", emptySlots(env.get(id)), []string{"2 弹幕源 b"})
	})
}
