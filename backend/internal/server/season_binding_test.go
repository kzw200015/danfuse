package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// 假适配器的合集能力：链接 "fakelist/<名字>" 识别为一个种类为 list 的候选 {"list":"<名字>"}。
// ListCollection 对名字 gone 返回 NotFound、对 down 返回 Upstream；其余返回三个条目：
// 序号为 1 的 <名字>1、序号为 2 的 <名字>2、平台没给出整数集号的 <名字>sp，弹幕源 ref 与贴链接 "fake/<条目名>" 相同。

type fakeListRef struct {
	List string `json:"list"`
}

func (fakeAdapter) ParseCollectionLink(_ context.Context, link string) ([]source.CollectionCandidate, error) {
	name, ok := strings.CutPrefix(link, "fakelist/")
	if !ok {
		return nil, source.ErrUnrecognized
	}
	ref, err := json.Marshal(fakeListRef{List: name})
	return []source.CollectionCandidate{{Kind: "list", Ref: ref}}, err
}

func (fakeAdapter) ListCollection(_ context.Context, ref source.CollectionRef) (source.Collection, error) {
	var r fakeListRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Collection{}, err
	}
	switch r.List {
	case "gone":
		return source.Collection{}, &source.Error{Kind: source.NotFound, Message: "合集不存在或已删除"}
	case "down":
		return source.Collection{}, &source.Error{Kind: source.Upstream, Message: "B 站接口异常"}
	}
	item := func(name string) source.Ref { return source.Ref(`{"name":"` + r.List + name + `"}`) }
	return source.Collection{Title: "合集 " + r.List, Items: []source.CollectionItem{
		{Ref: item("1"), Number: 1, Label: r.List + "1"},
		{Ref: item("2"), Number: 2, Label: r.List + "2"},
		{Ref: item("sp"), Unmatched: "集号「SP」不是整数", Label: r.List + "sp"},
	}}, nil
}

func (fakeAdapter) DescribeCollection(ref source.CollectionRef) (source.Display, error) {
	var r fakeListRef
	err := json.Unmarshal(ref, &r)
	return source.Display{URL: "https://fake.test/list/" + r.List, Label: "假合集 " + r.List}, err
}

// seasonBindingServer 在 synctest 气泡里新建连接池，写入 seedCatalog 的目录，在后台运行 SeasonBindingService，
// 起完整的 Echo（目录、绑定、季绑定接口），源适配器只注册了 fakeAdapter。测试结束时先停下后台循环，再关闭连接池。
func seasonBindingServer(t *testing.T, cfg *pgxpool.Config) (*Server, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.Open(t, cfg)
	seedCatalog(t, pool)

	logger := slog.New(slog.DiscardHandler)
	env := testenv.New(pool, logger, fakeAdapter{})
	testenv.RunInBackground(t, env.SeasonBindings)

	return New(config.Server{}, config.Dandanplay{}, logger, &Handlers{
		Catalog:       catalog.NewHandler(env.Catalog),
		Binding:       binding.NewHandler(env.Bindings, uploadLimits),
		SeasonBinding: seasonbinding.NewHandler(env.SeasonBindings, uploadLimits),
	}), pool
}

// TestSeasonBindingAPI 合集的季绑定：创建、详情、开关追更、立即补建、删除，以及剧详情、剧列表里的季绑定。
// 种子目录的第 1 季（季 1）有第 1 集（集 2，没有绑定）和第 2 集（集 1，已有两个绑定）。
func TestSeasonBindingAPI(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, pool := seasonBindingServer(t, cfg)
		defaultPatterns := jsonString(source.DefaultEpisodeRule().Patterns())

		// 默认的集号规则
		_, _, data := call(t, srv, http.MethodGet, "/api/episode-rules/default", "", http.StatusOK)
		testenv.AssertJSON(t, data, `{"episodePatterns": `+defaultPatterns+`}`)

		// 创建：201 和详情，只有合集的字段，不含原始 ref；补建在后台进行
		_, _, data = call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings/collection",
			`{"link": " fakelist/s ", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": `+defaultPatterns+`}`, http.StatusCreated)
		wantFields := []string{
			"adapter", "bindingCount", "createdAt", "emptySlots", "episodePatterns", "follow", "id", "items", "kind", "lastCheckedAt", "lastError",
			"link", "mappingFrom", "mappingTo", "numberedByRule", "running", "seasonId", "sourceLabel", "sourceUrl", "status", "title",
		}
		if got := slices.Sorted(maps.Keys(decodeObject(t, data))); !slices.Equal(got, wantFields) {
			t.Errorf("季绑定的字段 = %q\nwant %q", got, wantFields)
		}
		synctest.Wait()

		_, _, data = call(t, srv, http.MethodGet, "/api/season-bindings/1", "", http.StatusOK)
		detail := decodeObject(t, data)
		popTime(t, detail, "lastCheckedAt") // 这一轮的开始时间
		popTime(t, detail, "createdAt")
		testenv.AssertJSON(t, json.RawMessage(jsonString(detail)), `{
			"id": 1, "seasonId": 1, "kind": "collection", "adapter": "fake", "link": "fakelist/s",
			"sourceUrl": "https://fake.test/list/s", "sourceLabel": "假合集 s",
			"title": "合集 s", "mappingFrom": 1, "mappingTo": 1, "numberedByRule": false, "episodePatterns": `+defaultPatterns+`,
			"follow": true, "status": "active", "lastError": null, "running": false, "bindingCount": 2,
			"items": [
				{"label": "s1", "number": 1, "episodeNumber": 1, "status": "keep", "reason": null, "replaces": null, "lastError": null, "lastErrorAt": null},
				{"label": "s2", "number": 2, "episodeNumber": 2, "status": "keep", "reason": null, "replaces": null, "lastError": null, "lastErrorAt": null},
				{"label": "ssp", "number": null, "episodeNumber": null, "status": "unmatched", "reason": "平台没有给出整数集号", "replaces": null,
				 "lastError": null, "lastErrorAt": null}
			],
			"emptySlots": []
		}`)

		// 剧详情：季带着季绑定（不含条目表），建出的绑定带着季绑定 ID；剧列表带追更标记
		_, _, data = call(t, srv, http.MethodGet, "/api/series/1", "", http.StatusOK)
		var series struct {
			Seasons []struct {
				Number         int               `json:"number"`
				SeasonBindings []json.RawMessage `json:"seasonBindings"`
				Episodes       []struct {
					Bindings []struct {
						SeasonBindingID *int64 `json:"seasonBindingId"`
					} `json:"bindings"`
				} `json:"episodes"`
			} `json:"seasons"`
		}
		if err := json.Unmarshal(data, &series); err != nil {
			t.Fatal(err)
		}
		season := series.Seasons[1]
		if season.Number != 1 || len(season.SeasonBindings) != 1 {
			t.Fatalf("第 1 季的季绑定 = %s", season.SeasonBindings)
		}
		if _, ok := decodeObject(t, season.SeasonBindings[0])["items"]; ok {
			t.Error("剧详情里的季绑定不应带条目表")
		}
		var sources []string
		for _, e := range season.Episodes {
			for _, b := range e.Bindings {
				sources = append(sources, jsonString(b.SeasonBindingID))
			}
		}
		if want := []string{"1", "null", "null", "1"}; !slices.Equal(sources, want) {
			t.Errorf("第 1 季各绑定的 seasonBindingId = %q, want %q", sources, want)
		}
		following := func() string {
			t.Helper()
			_, _, data := call(t, srv, http.MethodGet, "/api/series", "", http.StatusOK)
			var list []struct {
				Following bool `json:"following"`
			}
			if err := json.Unmarshal(data, &list); err != nil {
				t.Fatal(err)
			}
			return jsonString(list)
		}
		if got := following(); got != `[{"following":true},{"following":false},{"following":false}]` {
			t.Errorf("剧列表的追更标记 = %s", got)
		}

		// 关掉追更：返回详情
		_, _, data = call(t, srv, http.MethodPatch, "/api/season-bindings/1", `{"follow": false}`, http.StatusOK)
		if got := decodeObject(t, data); string(got["follow"]) != "false" || string(got["mappingFrom"]) != "1" {
			t.Errorf("PATCH 之后 follow = %s, mappingFrom = %s", got["follow"], got["mappingFrom"])
		}
		if got := following(); got != `[{"following":false},{"following":false},{"following":false}]` {
			t.Errorf("关掉追更后的追更标记 = %s", got)
		}

		// 立即补建：202 和详情（补建刚开始）；正在补建时 409
		_, _, data = call(t, srv, http.MethodPost, "/api/season-bindings/1/backfill", "", http.StatusAccepted)
		if got := decodeObject(t, data); string(got["running"]) != "true" || string(got["id"]) != "1" {
			t.Errorf("立即补建返回 running = %s、id = %s", got["running"], got["id"])
		}
		synctest.Wait()
		lease, ok, err := database.TryLease(t.Context(), pool, slog.New(slog.DiscardHandler), database.LeaseSeasonBackfill(1))
		if err != nil || !ok {
			t.Fatalf("TryLease = %v, %v", ok, err)
		}
		assertAPIErrors(t, srv, []apiError{{http.MethodPost, "/api/season-bindings/1/backfill", "", http.StatusConflict, "正在补建"}})
		lease.Release()

		// 删除：它建出的绑定一起删掉，见 service 的 TestDeleteSeasonBinding
		if code, _, data := call(t, srv, http.MethodDelete, "/api/season-bindings/1", "", http.StatusOK); code != 0 || string(data) != "null" {
			t.Errorf("DELETE: code=%d data=%s, want 0 null", code, data)
		}
		call(t, srv, http.MethodGet, "/api/season-bindings/1", "", http.StatusNotFound)
	})
}

// TestCollectionPreviewAndSaveAPI 合集的新建预览、编辑预览和保存表单。种子目录同 TestSeasonBindingAPI，
// 创建后合集的 s1、s2 分别建在第 1 集（绑定 5）、第 2 集（绑定 6）。
func TestCollectionPreviewAndSaveAPI(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, pool := seasonBindingServer(t, cfg)
		patterns := jsonString(source.DefaultEpisodeRule().Patterns())

		// 新建的预览：每个候选一组，条目里没有失败原因的字段
		_, _, data := call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings/collection/preview",
			`{"link": " fakelist/s ", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": `+patterns+`}`, http.StatusOK)
		testenv.AssertJSON(t, data, `{"candidates": [{
			"collectionKind": "list", "title": "合集 s", "sourceUrl": "https://fake.test/list/s", "sourceLabel": "假合集 s",
			"finished": false, "numberedByRule": false, "current": false,
			"items": [
				{"label": "s1", "number": 1, "episodeNumber": 1, "status": "create", "reason": null, "replaces": null},
				{"label": "s2", "number": 2, "episodeNumber": 2, "status": "create", "reason": null, "replaces": null},
				{"label": "ssp", "number": null, "episodeNumber": null, "status": "unmatched", "reason": "集号「SP」不是整数", "replaces": null}
			],
			"removed": [],
			"summary": {"create": 2, "replace": 0, "remove": 0, "keep": 0}
		}]}`)

		call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings/collection",
			`{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": `+patterns+`}`, http.StatusCreated)
		synctest.Wait()

		// 编辑的预览：改集号对应，第 2 集的条目改对到第 1 集，替换那里的绑定；第 2 集的槽位保存后删除
		shifted := `{"link": "fakelist/s", "mappingFrom": 2, "mappingTo": 1, "episodePatterns": ` + patterns + `}`
		_, _, data = call(t, srv, http.MethodPost, "/api/season-bindings/1/collection/preview", shifted, http.StatusOK)
		testenv.AssertJSON(t, data, `{"candidates": [{
			"collectionKind": "list", "title": "合集 s", "sourceUrl": "https://fake.test/list/s", "sourceLabel": "假合集 s",
			"finished": false, "numberedByRule": false, "current": true,
			"items": [
				{"label": "s1", "number": 1, "episodeNumber": null, "status": "beforeStart", "reason": null, "replaces": null},
				{"label": "s2", "number": 2, "episodeNumber": 1, "status": "replace", "reason": null,
				 "replaces": {"episodeNumber": 1, "bindingId": 5, "title": "弹幕源 s1", "dead": false}},
				{"label": "ssp", "number": null, "episodeNumber": null, "status": "unmatched", "reason": "集号「SP」不是整数", "replaces": null}
			],
			"removed": [{"episodeNumber": 2, "bindingId": 6, "title": "弹幕源 s2", "dead": false}],
			"summary": {"create": 0, "replace": 1, "remove": 1, "keep": 0}
		}]}`)

		// 保存：返回详情；删掉第 2 集的槽位，替换交给随后的补建
		_, _, data = call(t, srv, http.MethodPut, "/api/season-bindings/1/collection", shifted, http.StatusOK)
		if got := decodeObject(t, data); string(got["mappingFrom"]) != "2" || string(got["emptySlots"]) != "[]" {
			t.Errorf("保存返回 mappingFrom = %s、emptySlots = %s", got["mappingFrom"], got["emptySlots"])
		}
		synctest.Wait()
		bound := testenv.QueryInt(t, pool, `SELECT count(*) FROM bindings WHERE season_binding_id = 1 AND episode_id = 2 AND ref->>'name' = 's2'`)
		if n := testenv.QueryInt(t, pool, `SELECT count(*) FROM bindings WHERE season_binding_id = 1`); n != 1 || bound != 1 {
			t.Errorf("补建之后季绑定有 %d 个绑定、第 1 集上 s2 的 %d 个，want 1、1", n, bound)
		}

		// 保存时正在补建：409
		lease, ok, err := database.TryLease(t.Context(), pool, slog.New(slog.DiscardHandler), database.LeaseSeasonBackfill(1))
		if err != nil || !ok {
			t.Fatalf("TryLease = %v, %v", ok, err)
		}
		assertAPIErrors(t, srv, []apiError{{http.MethodPut, "/api/season-bindings/1/collection", shifted, http.StatusConflict, "正在补建或保存，稍后再试"}})
		lease.Release()

		folder, _ := testenv.InsertFolderSeasonBinding(t, pool, 1, "来自新世界", 1, 1,
			testenv.FolderEntry{Label: "1", Files: []binding.UploadedFile{testenv.Snapshot1("1.xml")}})
		folderPath := "/api/season-bindings/" + jsonString(folder)
		assertAPIErrors(t, srv, []apiError{
			{http.MethodPost, "/api/seasons/0/season-bindings/collection/preview", shifted, http.StatusBadRequest, "季 ID 不合法"},
			{http.MethodPost, "/api/seasons/1/season-bindings/collection/preview", `{"link": " "}`, http.StatusBadRequest, "请粘贴合集的链接"},
			{http.MethodPost, "/api/seasons/1/season-bindings/collection/preview", `{"link": "fakelist/s", "mappingFrom": 1}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPost, "/api/seasons/99/season-bindings/collection/preview", shifted, http.StatusNotFound, "季不存在"},
			{http.MethodPost, "/api/seasons/1/season-bindings/collection/preview", `{"link": "fakelist/gone", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusUnprocessableEntity, "合集不存在或已删除"},

			{http.MethodPost, "/api/season-bindings/0/collection/preview", shifted, http.StatusBadRequest, "季绑定 ID 不合法"},
			{http.MethodPost, "/api/season-bindings/1/collection/preview", `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1}`, http.StatusBadRequest, "至少要有一条集号规则"},
			{http.MethodPost, "/api/season-bindings/99/collection/preview", shifted, http.StatusNotFound, "季绑定不存在"},
			{http.MethodPost, folderPath + "/collection/preview", shifted, http.StatusBadRequest, "这不是合集的季绑定"},

			{http.MethodPut, "/api/season-bindings/0/collection", shifted, http.StatusBadRequest, "季绑定 ID 不合法"},
			{http.MethodPut, "/api/season-bindings/1/collection", `{"link": ""}`, http.StatusBadRequest, "请粘贴合集的链接"},
			{http.MethodPut, "/api/season-bindings/1/collection", `{"link": "fakelist/s", "collectionKind": "pages", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusBadRequest, "链接里没有这种合集，请重新预览"},
			{http.MethodPut, "/api/season-bindings/99/collection", shifted, http.StatusNotFound, "季绑定不存在"},
			{http.MethodPut, folderPath + "/collection", shifted, http.StatusBadRequest, "这不是合集的季绑定"},
		})
	})
}

// TestFolderSeasonBindingAPI 文件夹的季绑定的详情：只有文件夹的字段，条目名称是"季绑定标题 / 条目名称"；没有追更。
func TestFolderSeasonBindingAPI(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, pool := seasonBindingServer(t, cfg)
		id, _ := testenv.InsertFolderSeasonBinding(t, pool, 1, "来自新世界", 1, 1,
			testenv.FolderEntry{Label: "1", Files: []binding.UploadedFile{testenv.Snapshot1("1.xml")}})
		path := "/api/season-bindings/" + jsonString(id)

		_, _, data := call(t, srv, http.MethodGet, path, "", http.StatusOK)
		detail := decodeObject(t, data)
		popTime(t, detail, "createdAt")
		testenv.AssertJSON(t, json.RawMessage(jsonString(detail)), `{
			"id": `+jsonString(id)+`, "seasonId": 1, "kind": "folder", "title": "来自新世界",
			"episodePatterns": `+jsonString(source.DefaultEpisodeRule().Patterns())+`, "mappingFrom": 1, "mappingTo": 1,
			"bindingCount": 0, "entryCount": 1,
			"items": [
				{"label": "来自新世界 / 1", "number": 1, "episodeNumber": 1, "status": "create", "reason": null, "replaces": null,
				 "lastError": null, "lastErrorAt": null}
			],
			"emptySlots": []
		}`)

		assertAPIErrors(t, srv, []apiError{
			{http.MethodPatch, path, `{"follow": true}`, http.StatusBadRequest, "这不是合集的季绑定"},
		})
		call(t, srv, http.MethodDelete, path, "", http.StatusOK)
	})
}

func TestSeasonBindingAPIErrors(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, _ := seasonBindingServer(t, cfg)
		const create = "/api/seasons/1/season-bindings/collection"
		call(t, srv, http.MethodPost, create, `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusCreated)
		synctest.Wait()

		assertAPIErrors(t, srv, []apiError{
			{http.MethodPost, "/api/seasons/0/season-bindings/collection", `{"link": "fakelist/s"}`, http.StatusBadRequest, "季 ID 不合法"},
			{http.MethodPost, create, `{"link": "  "}`, http.StatusBadRequest, "请粘贴合集的链接"},
			{http.MethodPost, create, `{"link": `, http.StatusBadRequest, "请求参数错误"},
			{http.MethodPost, create, `{"link": "fakelist/t", "mappingTo": 1}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPost, create, `{"link": "fakelist/t", "mappingFrom": 1, "mappingTo": -1}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPost, create, `{"link": "fakelist/t", "mappingFrom": 1.5, "mappingTo": 1}`, http.StatusBadRequest, "请求参数错误"},
			{http.MethodPost, create, `{"link": "fakelist/t", "mappingFrom": 1, "mappingTo": 1}`, http.StatusBadRequest, "至少要有一条集号规则"},
			{http.MethodPost, create, `{"link": "fakelist/t", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(("]}`, http.StatusBadRequest, "第 1 条集号规则不是合法的正则：missing closing )"},
			{http.MethodPost, create, `{"link": "fakelist/t", "collectionKind": "pages", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusBadRequest, "链接里没有这种合集，请重新预览"},
			{http.MethodPost, create, `{"link": "https://example.com/v/1", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusBadRequest, "无法识别的链接"},
			{http.MethodPost, create, `{"link": "fakelist/gone", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusUnprocessableEntity, "合集不存在或已删除"},
			{http.MethodPost, create, `{"link": "fakelist/down", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusBadGateway, "B 站接口异常"},
			{http.MethodPost, create, `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusConflict, "这一季已经绑定过这个合集"},
			{http.MethodPost, "/api/seasons/99/season-bindings/collection", `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusNotFound, "季不存在"},

			{http.MethodGet, "/api/season-bindings/0", "", http.StatusBadRequest, "季绑定 ID 不合法"},
			{http.MethodGet, "/api/season-bindings/abc", "", http.StatusBadRequest, "请求参数错误"},
			{http.MethodGet, "/api/season-bindings/99", "", http.StatusNotFound, "季绑定不存在"},

			{http.MethodPatch, "/api/season-bindings/1", `{}`, http.StatusBadRequest, "没有要修改的内容"},
			{http.MethodPatch, "/api/season-bindings/1", `{"follow": "yes"}`, http.StatusBadRequest, "请求参数错误"},
			{http.MethodPatch, "/api/season-bindings/99", `{"follow": true}`, http.StatusNotFound, "季绑定不存在"},

			{http.MethodPost, "/api/season-bindings/0/backfill", "", http.StatusBadRequest, "季绑定 ID 不合法"},
			{http.MethodPost, "/api/season-bindings/99/backfill", "", http.StatusNotFound, "季绑定不存在"},

			{http.MethodDelete, "/api/season-bindings/0", "", http.StatusBadRequest, "季绑定 ID 不合法"},
			{http.MethodDelete, "/api/season-bindings/99", "", http.StatusNotFound, "季绑定不存在"},
		})
	})
}
