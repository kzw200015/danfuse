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

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/handler"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
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
	return source.Collection{Title: "合集 " + r.List, Items: []source.CollectionItem{
		{Ref: item("1"), Number: 1, Label: r.List + "1"},
		{Ref: item("2"), Number: 2, Label: r.List + "2", Note: "共 2 个分 P，只用 P1"},
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
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	seedCatalog(t, pool)

	store := repository.NewStore(pool)
	sources := source.NewRegistry(fakeAdapter{})
	logger := slog.New(slog.DiscardHandler)
	bindings := service.NewBindingService(store, sources, logger)
	svc := service.NewSeasonBindingService(store, pool, sources, bindings, logger)
	runInBackground(t, svc)

	return New(config.Server{}, config.Dandanplay{}, logger, &handler.Handlers{
		Catalog:       handler.NewCatalogHandler(service.NewCatalogService(store, sources)),
		Binding:       handler.NewBindingHandler(bindings, uploadLimits),
		SeasonBinding: handler.NewSeasonBindingHandler(svc),
	}, nil), pool
}

// TestSeasonBindingAPI 预览、创建、详情、开关追更、立即补建、删除，以及剧详情、剧列表里的季绑定。
// 种子目录的第 1 季（季 1）有第 1 集（集 2，没有绑定）和第 2 集（集 1，已有两个绑定）。
func TestSeasonBindingAPI(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, pool := seasonBindingServer(t, cfg)

		// 预览：不保存，重复的序号已标出，给出默认的集号对应
		_, _, data := call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": " fakelist/s "}`, http.StatusOK)
		assertJSON(t, data, `{"candidates": [{
			"kind": "list", "title": "合集 s", "sourceUrl": "https://fake.test/list/s", "sourceLabel": "假合集 s",
			"finished": false, "mappingFrom": 1, "mappingTo": 1,
			"items": [
				{"label": "s1", "note": null, "number": 1, "reason": null},
				{"label": "s2", "note": "共 2 个分 P，只用 P1", "number": 2, "reason": null},
				{"label": "ssp", "note": null, "number": null, "reason": "集号「SP」不是整数"}
			]
		}]}`)

		// 创建：201 和详情，不含原始 ref；补建在后台进行
		_, _, data = call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings",
			`{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1}`, http.StatusCreated)
		wantFields := []string{
			"adapter", "bindingCount", "finished", "follow", "id", "items", "lastCheckedAt", "lastError",
			"mappingFrom", "mappingTo", "running", "seasonId", "sourceLabel", "sourceUrl", "status", "title",
		}
		if got := slices.Sorted(maps.Keys(decodeObject(t, data))); !slices.Equal(got, wantFields) {
			t.Errorf("季绑定的字段 = %q\nwant %q", got, wantFields)
		}
		synctest.Wait()

		_, _, data = call(t, srv, http.MethodGet, "/api/season-bindings/1", "", http.StatusOK)
		detail := decodeObject(t, data)
		if string(detail["lastCheckedAt"]) == "null" {
			t.Error("lastCheckedAt 为 null，want 这一轮的开始时间")
		}
		delete(detail, "lastCheckedAt")
		assertJSON(t, json.RawMessage(jsonString(detail)), `{
			"id": 1, "seasonId": 1, "adapter": "fake", "sourceUrl": "https://fake.test/list/s", "sourceLabel": "假合集 s",
			"title": "合集 s", "finished": false, "mappingFrom": 1, "mappingTo": 1, "follow": true, "status": "active",
			"lastError": null, "running": false, "bindingCount": 2,
			"items": [
				{"label": "s1", "note": null, "number": 1, "state": "bound", "reason": null, "episodeNumber": 1, "lastErrorAt": null},
				{"label": "s2", "note": "共 2 个分 P，只用 P1", "number": 2, "state": "bound", "reason": null, "episodeNumber": 2, "lastErrorAt": null},
				{"label": "ssp", "note": null, "number": null, "state": "unmatched", "reason": "集号「SP」不是整数", "episodeNumber": null, "lastErrorAt": null}
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
		if code, message, _ := call(t, srv, http.MethodPost, "/api/season-bindings/1/backfill", "", http.StatusConflict); code != 1 || message != "正在补建" {
			t.Errorf("正在补建时：code=%d message=%q", code, message)
		}
		_, _, data = call(t, srv, http.MethodGet, "/api/season-bindings/1", "", http.StatusOK)
		if got := decodeObject(t, data)["running"]; string(got) != "true" {
			t.Errorf("持有租约时 running = %s, want true", got)
		}
		lease.Release()

		// 删除，一起删掉建出的绑定
		call(t, srv, http.MethodDelete, "/api/season-bindings/1?withBindings=true", "", http.StatusOK)
		var n int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM bindings WHERE episode_id IN (1, 2)`).Scan(&n); err != nil || n != 2 {
			t.Errorf("第 1 季剩下 %d 个绑定（%v），want 只剩原来的 2 个", n, err)
		}
		call(t, srv, http.MethodGet, "/api/season-bindings/1", "", http.StatusNotFound)
	})
}

func TestSeasonBindingAPIErrors(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv, _ := seasonBindingServer(t, cfg)
		call(t, srv, http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1}`, http.StatusCreated)
		synctest.Wait()

		for _, tt := range []struct {
			method, target, body string
			wantStatus           int
			wantMessage          string
		}{
			{http.MethodPost, "/api/seasons/0/season-bindings/preview", `{"link": "fakelist/s"}`, http.StatusBadRequest, "季 ID 不合法"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": "  "}`, http.StatusBadRequest, "请粘贴合集的链接"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": `, http.StatusBadRequest, "请求参数错误"},
			{http.MethodPost, "/api/seasons/99/season-bindings/preview", `{"link": "fakelist/s"}`, http.StatusNotFound, "季不存在"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": "https://example.com/v/1"}`, http.StatusBadRequest, "无法识别的链接"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": "fakelist/gone"}`, http.StatusUnprocessableEntity, "合集不存在或已删除"},
			{http.MethodPost, "/api/seasons/1/season-bindings/preview", `{"link": "fakelist/down"}`, http.StatusBadGateway, "B 站接口异常"},

			{http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/t", "mappingTo": 1}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/t", "mappingFrom": 1, "mappingTo": -1}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/t", "mappingFrom": 1.5, "mappingTo": 1}`, http.StatusBadRequest, "请求参数错误"},
			{http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/t", "kind": "pages", "mappingFrom": 1, "mappingTo": 1}`, http.StatusBadRequest, "链接里没有这种合集，请重新预览"},
			{http.MethodPost, "/api/seasons/1/season-bindings", `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1}`, http.StatusConflict, "这一季已经绑定过这个合集"},
			{http.MethodPost, "/api/seasons/99/season-bindings", `{"link": "fakelist/s", "mappingFrom": 1, "mappingTo": 1}`, http.StatusNotFound, "季不存在"},

			{http.MethodGet, "/api/season-bindings/0", "", http.StatusBadRequest, "季绑定 ID 不合法"},
			{http.MethodGet, "/api/season-bindings/abc", "", http.StatusBadRequest, "请求参数错误"},
			{http.MethodGet, "/api/season-bindings/99", "", http.StatusNotFound, "季绑定不存在"},

			{http.MethodPatch, "/api/season-bindings/1", `{}`, http.StatusBadRequest, "没有要修改的内容"},
			{http.MethodPatch, "/api/season-bindings/1", `{"mappingFrom": -1}`, http.StatusBadRequest, "集号对应必须是不小于 0 的整数"},
			{http.MethodPatch, "/api/season-bindings/99", `{"follow": true}`, http.StatusNotFound, "季绑定不存在"},

			{http.MethodPost, "/api/season-bindings/99/backfill", "", http.StatusNotFound, "季绑定不存在"},

			{http.MethodDelete, "/api/season-bindings/1?withBindings=abc", "", http.StatusBadRequest, "请求参数错误"},
			{http.MethodDelete, "/api/season-bindings/99", "", http.StatusNotFound, "季绑定不存在"},
		} {
			if code, message, _ := call(t, srv, tt.method, tt.target, tt.body, tt.wantStatus); code != 1 || message != tt.wantMessage {
				t.Errorf("%s %s %s: code=%d message=%q, want %q", tt.method, tt.target, tt.body, code, message, tt.wantMessage)
			}
		}

		// 不一起删：建出的绑定留下，变成普通绑定
		call(t, srv, http.MethodDelete, "/api/season-bindings/1", "", http.StatusOK)
		_, _, data := call(t, srv, http.MethodGet, "/api/series/1", "", http.StatusOK)
		if got := strings.Count(string(data), `"seasonBindingId":null`); got != 5 {
			t.Errorf("删除季绑定后有 %d 个绑定的 seasonBindingId 为 null，want 这部剧的全部 5 个", got)
		}
	})
}
