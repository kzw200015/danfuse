package seasonbinding_test

import (
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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

		testenv.AssertStrings(t, "绑定", env.bindings(), []string{"1 a 1", "2 b -", "3 other -", "3 c 1"})
		testenv.AssertStrings(t, "拉取", src.FetchedNames(), []string{"a", "c"})
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

				testenv.AssertAppError(t, err, tt.wantStatus, tt.wantMessage)
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
		testenv.AssertAppError(t, err, http.StatusConflict, "这一季已经绑定过这个合集")
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
		testenv.AssertStrings(t, "季绑定", got, want)
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

		testenv.AssertAppError(t, <-errc, http.StatusNotFound, "这一季已被删除")
		if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM season_bindings`); n != 0 {
			t.Errorf("写入了 %d 个季绑定", n)
		}
	})
}
