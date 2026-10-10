package server

import (
	"context"
	"encoding/json"
	"fmt"
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
// 序号为 1 的 <名字>1、序号为 2 且有多个分 P 的 <名字>2、对不上的 <名字>sp，弹幕源 ref 与贴链接 "fake/<条目名>" 相同。

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
	if r.List == "ugc" { // 按集号规则编号的合集
		return source.Collection{Title: "投稿合集", NumberedByRule: true, Items: []source.CollectionItem{
			{Ref: item("a"), Label: "某番 第1集"},
			{Ref: item("b"), Label: "某番 [02]"},
		}}, nil
	}
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
		SeasonBinding: seasonbinding.NewHandler(env.SeasonBindings),
	}), pool
}

// TestSeasonBindingAPI 预览、创建、详情、开关追更、立即补建、删除，以及剧详情、剧列表里的季绑定。
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

		// 预览：不保存，重复的序号已标出
		_, _, data = call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": " fakelist/s ", "episodePatterns": `+defaultPatterns+`}`, http.StatusOK)
		testenv.AssertJSON(t, data, `{"candidates": [{
			"kind": "list", "title": "合集 s", "sourceUrl": "https://fake.test/list/s", "sourceLabel": "假合集 s",
			"finished": false, "numberedByRule": false,
			"items": [
				{"label": "s1", "number": 1, "reason": null},
				{"label": "s2", "number": 2, "reason": null},
				{"label": "ssp", "number": null, "reason": "集号「SP」不是整数"}
			]
		}]}`)

		// 按集号规则编号的合集：按请求里的规则认出序号（每条去掉前后的空白）
		_, _, data = call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings/preview",
			`{"link": "fakelist/ugc", "episodePatterns": [" \\[(\\d+)\\]$ "]}`, http.StatusOK)
		testenv.AssertJSON(t, data, `{"candidates": [{
			"kind": "list", "title": "投稿合集", "sourceUrl": "https://fake.test/list/ugc", "sourceLabel": "假合集 ugc",
			"finished": false, "numberedByRule": true,
			"items": [
				{"label": "某番 第1集", "number": null, "reason": "不符合集号规则"},
				{"label": "某番 [02]", "number": 2, "reason": null}
			]
		}]}`)

		// 创建：201 和详情，不含原始 ref；补建在后台进行
		_, _, data = call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings",
			`{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": `+defaultPatterns+`}`, http.StatusCreated)
		wantFields := []string{
			"adapter", "bindingCount", "createdAt", "episodePatterns", "finished", "follow", "id", "items", "kind", "lastCheckedAt", "lastError",
			"mappingFrom", "mappingTo", "numberedByRule", "running", "seasonId", "sourceLabel", "sourceUrl", "status", "title",
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
			"id": 1, "seasonId": 1, "kind": "collection", "adapter": "fake", "sourceUrl": "https://fake.test/list/s", "sourceLabel": "假合集 s",
			"title": "合集 s", "finished": false, "mappingFrom": 1, "mappingTo": 1, "numberedByRule": false, "episodePatterns": `+defaultPatterns+`,
			"follow": true, "status": "active", "lastError": null, "running": false, "bindingCount": 2,
			"items": [
				{"label": "s1", "number": 1, "state": "bound", "reason": null, "episodeNumber": 1, "lastErrorAt": null},
				{"label": "s2", "number": 2, "state": "bound", "reason": null, "episodeNumber": 2, "lastErrorAt": null},
				{"label": "ssp", "number": null, "state": "unmatched", "reason": "集号「SP」不是整数", "episodeNumber": null, "lastErrorAt": null}
			]
		}`)

		// 剧详情：季带着季绑定（不含条目表），补建出的绑定带着季绑定 ID；剧列表带追更标记
		_, _, data = call(t, srv, http.MethodGet, "/api/series/1", "", http.StatusOK)
		var series struct {
			Seasons []struct {
				Number         int               `json:"number"`
				SeasonBindings []json.RawMessage `json:"seasonBindings"`
				Episodes       []struct {
					Number   int `json:"number"`
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

		// 关掉追更：只改传了的字段
		_, _, data = call(t, srv, http.MethodPatch, "/api/season-bindings/1", `{"follow": false}`, http.StatusOK)
		if got := decodeObject(t, data); string(got["follow"]) != "false" || string(got["mappingFrom"]) != "1" {
			t.Errorf("PATCH 之后 follow = %s, mappingFrom = %s", got["follow"], got["mappingFrom"])
		}
		if got := following(); got != `[{"following":false},{"following":false},{"following":false}]` {
			t.Errorf("关掉追更后的追更标记 = %s", got)
		}

		// 立即补建：202；正在补建时 409
		if _, _, data := call(t, srv, http.MethodPost, "/api/season-bindings/1/backfill", "", http.StatusAccepted); string(data) != "null" {
			t.Errorf("立即补建返回 %s, want null", data)
		}
		synctest.Wait()
		lease, ok, err := database.TryLease(t.Context(), pool, slog.New(slog.DiscardHandler), database.LeaseSeasonBackfill(1))
		if err != nil || !ok {
			t.Fatalf("TryLease = %v, %v", ok, err)
		}
		// 正在补建时详情的 running 为 true，见 service 的 TestBackfillTwice
		assertAPIErrors(t, srv, []apiError{{http.MethodPost, "/api/season-bindings/1/backfill", "", http.StatusConflict, "正在补建"}})
		lease.Release()

		// 按集号规则编号的合集：创建时保存规则，改规则时条目随即按新规则重新认出序号
		_, _, data = call(t, srv, http.MethodPost, "/api/seasons/2/season-bindings",
			`{"link": "fakelist/ugc", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["\\[(\\d+)\\]$"]}`, http.StatusCreated)
		ugc := decodeObject(t, data)
		if string(ugc["numberedByRule"]) != "true" || string(ugc["episodePatterns"]) != `["\\[(\\d+)\\]$"]` {
			t.Errorf("创建的季绑定 numberedByRule = %s, episodePatterns = %s", ugc["numberedByRule"], ugc["episodePatterns"])
		}
		synctest.Wait()
		_, _, data = call(t, srv, http.MethodPatch, "/api/season-bindings/"+string(ugc["id"]),
			`{"episodePatterns": `+defaultPatterns+`}`, http.StatusOK)
		var patched struct {
			EpisodePatterns []string `json:"episodePatterns"`
			Items           []struct {
				Number *int `json:"number"`
			} `json:"items"`
		}
		if err := json.Unmarshal(data, &patched); err != nil {
			t.Fatal(err)
		}
		if got := jsonString(patched); got != `{"episodePatterns":`+defaultPatterns+`,"items":[{"number":1},{"number":null}]}` {
			t.Errorf("改回默认规则之后 = %s", got)
		}
		synctest.Wait()

		// 删除；一起删与不一起删时建出的绑定怎样处理，见 service 的 TestDeleteSeasonBinding
		if code, _, data := call(t, srv, http.MethodDelete, "/api/season-bindings/1?withBindings=true", "", http.StatusOK); code != 0 || string(data) != "null" {
			t.Errorf("DELETE: code=%d data=%s, want 0 null", code, data)
		}
		call(t, srv, http.MethodGet, "/api/season-bindings/1", "", http.StatusNotFound)
	})
}

// TestFolderSeasonBindingAPI 文件夹的季绑定的 JSON：带 kind，合集专属的字段为 null，条目表为空；只能删除。
func TestFolderSeasonBindingAPI(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, pool := seasonBindingServer(t, cfg)
		id := testenv.InsertFolderSeasonBinding(t, pool, 1, "来自新世界", 1, 2)
		path := fmt.Sprintf("/api/season-bindings/%d", id)

		_, _, data := call(t, srv, http.MethodGet, path, "", http.StatusOK)
		detail := decodeObject(t, data)
		popTime(t, detail, "createdAt") // 上传时间
		testenv.AssertJSON(t, json.RawMessage(jsonString(detail)), fmt.Sprintf(`{
			"id": %d, "seasonId": 1, "kind": "folder", "title": "来自新世界",
			"adapter": null, "sourceUrl": null, "sourceLabel": null, "finished": null, "mappingFrom": null, "mappingTo": null,
			"numberedByRule": null, "episodePatterns": null, "lastError": null, "lastCheckedAt": null,
			"follow": false, "status": "active", "running": false, "bindingCount": 2,
			"items": []
		}`, id))

		assertAPIErrors(t, srv, []apiError{
			{http.MethodPatch, path, `{"follow": true}`, http.StatusBadRequest, "文件夹的季绑定只能删除"},
			{http.MethodPost, path + "/backfill", "", http.StatusBadRequest, "文件夹的季绑定只能删除"},
		})
		call(t, srv, http.MethodDelete, path, "", http.StatusOK)
	})
}

func TestSeasonBindingAPIErrors(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, _ := seasonBindingServer(t, cfg)
		call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusCreated)
		synctest.Wait()

		assertAPIErrors(t, srv, []apiError{
			{http.MethodPost, "/api/seasons/0/season-bindings/preview", `{"link": "fakelist/s"}`, http.StatusBadRequest, "季 ID 不合法"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": "  "}`, http.StatusBadRequest, "请粘贴合集的链接"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": `, http.StatusBadRequest, "请求参数错误"},
			{http.MethodPost, "/api/seasons/99/season-bindings/preview", `{"link": "fakelist/s", "episodePatterns": ["(\\d+)"]}`, http.StatusNotFound, "季不存在"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": "https://example.com/v/1", "episodePatterns": ["(\\d+)"]}`, http.StatusBadRequest, "无法识别的链接"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": "fakelist/gone", "episodePatterns": ["(\\d+)"]}`, http.StatusUnprocessableEntity, "合集不存在或已删除"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": "fakelist/down", "episodePatterns": ["(\\d+)"]}`, http.StatusBadGateway, "B 站接口异常"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": "fakelist/s", "episodePatterns": ["(\\d+)", "第\\d+集"]}`, http.StatusBadRequest, `第 2 条集号规则里要有一个捕获组，例如 第(\d+)集`},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": "fakelist/s", "episodePatterns": []}`, http.StatusBadRequest, "至少要有一条集号规则"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": "fakelist/s"}`, http.StatusBadRequest, "至少要有一条集号规则"},

			{http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/t", "mappingTo": 1}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/t", "mappingFrom": 1, "mappingTo": -1}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/t", "mappingFrom": 1.5, "mappingTo": 1}`, http.StatusBadRequest, "请求参数错误"},
			{http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/t", "kind": "pages", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusBadRequest, "链接里没有这种合集，请重新预览"},
			{http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/t", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(("]}`, http.StatusBadRequest, "第 1 条集号规则不是合法的正则：missing closing )"},
			{http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusConflict, "这一季已经绑定过这个合集"},
			{http.MethodPost, "/api/seasons/99/season-bindings", `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1, "episodePatterns": ["(\\d+)"]}`, http.StatusNotFound, "季不存在"},

			{http.MethodGet, "/api/season-bindings/0", "", http.StatusBadRequest, "季绑定 ID 不合法"},
			{http.MethodGet, "/api/season-bindings/abc", "", http.StatusBadRequest, "请求参数错误"},
			{http.MethodGet, "/api/season-bindings/99", "", http.StatusNotFound, "季绑定不存在"},

			{http.MethodPatch, "/api/season-bindings/1", `{}`, http.StatusBadRequest, "没有要修改的内容"},
			{http.MethodPatch, "/api/season-bindings/1", `{"mappingFrom": -1}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPatch, "/api/season-bindings/1", `{"episodePatterns": [" "]}`, http.StatusBadRequest, "第 1 条集号规则是空的"},
			{http.MethodPatch, "/api/season-bindings/99", `{"follow": true}`, http.StatusNotFound, "季绑定不存在"},

			{http.MethodPost, "/api/season-bindings/99/backfill", "", http.StatusNotFound, "季绑定不存在"},

			{http.MethodDelete, "/api/season-bindings/1?withBindings=abc", "", http.StatusBadRequest, "请求参数错误"},
			{http.MethodDelete, "/api/season-bindings/99", "", http.StatusNotFound, "季绑定不存在"},
		})
	})
}
