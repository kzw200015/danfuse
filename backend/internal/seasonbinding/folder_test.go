package seasonbinding_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/seasonbinding"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// TestFolderSeasonBindingView 文件夹的季绑定的详情：照常可用，条目表为空，绑定数是它建出的、现存的绑定；
// 剧详情的季绑定列表里和合集的季绑定按创建顺序排在一起。
func TestFolderSeasonBindingView(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{
			Collections: map[string]source.Collection{"s": {Title: "某番剧", Items: []source.CollectionItem{entry("a", 1)}}},
			Videos:      testenv.FakeVideos("a"),
		}
		env := newSeasonEnv(t, pool, src, 1, 2)
		before := testenv.InsertFolderSeasonBinding(t, pool, 1, "来自新世界", 1, 2)
		env.create(1, 1)
		after := testenv.InsertFolderSeasonBinding(t, pool, 1, "来自新世界", 2)

		d := env.get(before)
		if d.Kind != "folder" || d.Title != "来自新世界" || d.BindingCount != 2 || d.Items == nil || len(d.Items) != 0 {
			t.Errorf("Get = %+v, want 文件夹的季绑定、两个绑定、空的条目表", d)
		}

		views, err := env.svc.ListBySeries(t.Context(), 1)
		if err != nil {
			t.Fatalf("ListBySeries: %v", err)
		}
		var got []string
		for _, v := range views[1] {
			got = append(got, v.Kind+" "+v.Title)
		}
		testenv.AssertStrings(t, "第 1 季的季绑定", got, []string{"folder 来自新世界", "collection 某番剧", "folder 来自新世界"})
		if views[1][2].ID != after || views[1][2].BindingCount != 1 {
			t.Errorf("后传的文件夹的季绑定 = %+v, want ID %d、一个绑定", views[1][2], after)
		}
	})
}

// TestFolderSeasonBindingDeleteOnly 文件夹的季绑定只能删除：改追更、集号对应、集号规则与立即补建都返回 400，什么都不改，也不补建。
func TestFolderSeasonBindingDeleteOnly(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{}
		env := newSeasonEnv(t, pool, src, 1)
		id := testenv.InsertFolderSeasonBinding(t, pool, 1, "来自新世界", 1)

		rule := source.DefaultEpisodeRule()
		for _, p := range []seasonbinding.UpdateParams{
			{Follow: new(true)},
			{Follow: new(false)},
			{MappingFrom: new(int32(2)), MappingTo: new(int32(1))},
			{Rule: &rule},
		} {
			_, err := env.svc.Update(t.Context(), id, p)
			testenv.AssertAppError(t, err, http.StatusBadRequest, "文件夹的季绑定只能删除")
		}
		testenv.AssertAppError(t, env.svc.Backfill(t.Context(), id), http.StatusBadRequest, "文件夹的季绑定只能删除")
		synctest.Wait()

		if d := env.get(id); d.Follow || d.LastCheckedAt != nil {
			t.Errorf("被拒绝之后 follow = %v、lastCheckedAt = %v", d.Follow, d.LastCheckedAt)
		}
		if n := src.ListCount(); n != 0 {
			t.Errorf("列出了 %d 次合集", n)
		}
	})
}

// TestDeleteFolderSeasonBinding 删除文件夹的季绑定：withBindings 时只删掉它建出的绑定，同一集上手动上传的文件绑定、
// 链接绑定、合集的季绑定建出的绑定都还在；不带时它建出的绑定留下，变成普通的文件绑定。
func TestDeleteFolderSeasonBinding(t *testing.T) {
	t.Parallel()
	for _, withBindings := range []bool{false, true} {
		t.Run(fmt.Sprintf("withBindings=%v", withBindings), func(t *testing.T) {
			t.Parallel()
			testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				src := &testenv.FakeCollector{
					Collections: map[string]source.Collection{"s": {Items: []source.CollectionItem{entry("a", 1), entry("b", 2)}}},
					Videos:      testenv.FakeVideos("a", "b"),
				}
				env := newSeasonEnv(t, pool, src, 1, 2)
				env.bindManually(1, "m")
				env.exec(`INSERT INTO bindings (episode_id, kind, title) VALUES (1, 'file', '手动上传')`)
				collection := env.create(1, 1).ID
				folder := testenv.InsertFolderSeasonBinding(t, pool, 1, "来自新世界", 1, 2)

				if err := env.svc.Delete(t.Context(), folder, withBindings); err != nil {
					t.Fatalf("Delete: %v", err)
				}

				want := []string{"1 m -", "1 手动上传 -", "1 a 1", "1 来自新世界 -", "2 b 1", "2 来自新世界 -"}
				if withBindings {
					want = []string{"1 m -", "1 手动上传 -", "1 a 1", "2 b 1"}
				}
				testenv.AssertStrings(t, "绑定", env.bindings(), want)
				if d := env.get(collection); d.BindingCount != 2 {
					t.Errorf("合集的季绑定建出的绑定数 = %d, want 2", d.BindingCount)
				}
				_, err := env.svc.Get(t.Context(), folder)
				testenv.AssertAppError(t, err, http.StatusNotFound, "季绑定不存在")
			})
		})
	}
}

// TestFollowSkipsFolderSeasonBinding 追更的扫描不碰文件夹的季绑定：满了检查周期、季里有了新的集也不检查。
func TestFollowSkipsFolderSeasonBinding(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{}
		env := newSeasonEnv(t, pool, src, 1)
		id := testenv.InsertFolderSeasonBinding(t, pool, 1, "来自新世界", 1)

		time.Sleep(2 * testFollow.CheckInterval)
		env.addEpisode(2)
		time.Sleep(testFollow.ScanInterval)
		synctest.Wait()

		if d := env.get(id); d.LastCheckedAt != nil || d.LastError != nil || d.Status != "active" {
			t.Errorf("扫描之后 lastCheckedAt = %v、lastError = %v、status = %s", d.LastCheckedAt, d.LastError, d.Status)
		}
		if n := src.ListCount(); n != 0 {
			t.Errorf("列出了 %d 次合集", n)
		}
		if logs := env.logs.String(); strings.Contains(logs, "level=ERROR") {
			t.Errorf("扫描出错：\n%s", logs)
		}
	})
}
