package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/handler"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// catalogServer 起完整的 Echo，目录与绑定接口连到 pool，源适配器只注册了 fakeAdapter。
func catalogServer(pool *pgxpool.Pool) *Server {
	store := repository.NewStore(pool)
	sources := source.NewRegistry(fakeAdapter{})
	logger := slog.New(slog.DiscardHandler)
	return New(config.Server{}, config.Dandanplay{}, logger, &handler.Handlers{
		Catalog: handler.NewCatalogHandler(service.NewCatalogService(store, sources)),
		Binding: handler.NewBindingHandler(service.NewBindingService(store, sources, logger)),
	}, nil)
}

// posterPNG 种子目录里星海旅人的海报（图片 1）。
var posterPNG = []byte("\x89PNG\r\n\x1a\n星海旅人的海报")

// seedCatalog 写入一个小目录和几个绑定。每个测试的库都从模板新建，ID 从 1 开始，按插入顺序分配（注释里标出）。
// 季和集故意不按编号顺序插入，用来检查接口按编号排序，而不是按 ID。
func seedCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO images (content_type, data, sha256) VALUES ('image/png', $1, sha256($1))`, posterPNG)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), `
		INSERT INTO series (type, title, original_title, year, poster_image_id) VALUES
			('tv', '星海旅人', 'Star Voyager', 2019, 1), -- 剧 1：海报是图片 1
			('movie', '长夜灯塔', NULL, 2020, NULL),     -- 剧 2：没有海报
			('tv', '空无一季', NULL, NULL, NULL);        -- 剧 3：没有季
		INSERT INTO seasons (series_id, number, title) VALUES
			(1, 1, '第 1 季'), -- 季 1
			(1, 0, NULL),      -- 季 2：特别篇
			(1, 2, '第 2 季'), -- 季 3：没有集
			(2, 1, NULL);      -- 季 4
		INSERT INTO episodes (season_id, number, title, duration) VALUES
			(1, 2, '归航', 1440), -- 集 1
			(1, 1, '启程', 1420), -- 集 2
			(2, 1, NULL, NULL),   -- 集 3
			(4, 1, NULL, 5400);   -- 集 4
		-- 集 1 有两个绑定（一个失效），集 3 的绑定失效，集 2 没有绑定
		INSERT INTO bindings (episode_id, adapter, ref, title, duration, "offset", status, danmaku_count) VALUES
			(1, 'fake', '{"name": "a"}', '弹幕源 a', 1440, 1.5, 'active', 2), -- 绑定 1
			(1, 'fake', '{"name": "b"}', '弹幕源 b', 1380, 0, 'dead', 0),     -- 绑定 2
			(3, 'fake', '{"name": "c"}', '弹幕源 c', 600, 0, 'dead', 0),      -- 绑定 3
			(4, 'fake', '{"name": "d"}', '弹幕源 d', 5400, -2, 'active', 0);  -- 绑定 4
		INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text) VALUES
			(1, 1, 0, 1, 0, '前排'),
			(1, 2, 1500, 1, 16777215, '来了');
	`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestListSeries(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	srv := catalogServer(pool)

	if _, _, data := call(t, srv, http.MethodGet, "/api/series", "", http.StatusOK); string(data) != "[]" {
		t.Errorf("目录为空时 = %s, want []", data)
	}

	seedCatalog(t, pool)
	_, _, data := call(t, srv, http.MethodGet, "/api/series", "", http.StatusOK)
	assertJSON(t, data, `[
		{"id": 1, "type": "tv", "title": "星海旅人", "originalTitle": "Star Voyager", "year": 2019,
		 "posterImageId": 1, "seasonCount": 3, "episodeCount": 3,
		 "boundEpisodeCount": 2, "bindingCount": 3, "deadBindingCount": 2, "following": false},
		{"id": 2, "type": "movie", "title": "长夜灯塔", "originalTitle": null, "year": 2020,
		 "posterImageId": null, "seasonCount": 1, "episodeCount": 1,
		 "boundEpisodeCount": 1, "bindingCount": 1, "deadBindingCount": 0, "following": false},
		{"id": 3, "type": "tv", "title": "空无一季", "originalTitle": null, "year": null,
		 "posterImageId": null, "seasonCount": 0, "episodeCount": 0,
		 "boundEpisodeCount": 0, "bindingCount": 0, "deadBindingCount": 0, "following": false}
	]`)
}

func TestGetSeries(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	tests := []struct {
		target string
		want   string
	}{
		// 季按季号、集按集号、绑定按创建顺序排序；没有集的季、没有季的剧、没有绑定的集输出空数组。
		// 绑定的弹幕源链接和标签由适配器生成，不输出 ref 和 contentVersion
		{"/api/series/1", `{
			"id": 1, "type": "tv", "title": "星海旅人", "originalTitle": "Star Voyager", "year": 2019, "posterImageId": 1,
			"seasons": [
				{"id": 2, "number": 0, "title": null, "seasonBindings": [], "episodes": [
					{"id": 3, "number": 1, "title": null, "duration": null, "bindings": [
						{"id": 3, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/c", "sourceLabel": "假弹幕源 c",
						 "title": "弹幕源 c", "duration": 600, "offset": 0, "status": "dead", "danmakuCount": 0, "seasonBindingId": null, "lastFetchedAt": null}
					]}
				]},
				{"id": 1, "number": 1, "title": "第 1 季", "seasonBindings": [], "episodes": [
					{"id": 2, "number": 1, "title": "启程", "duration": 1420, "bindings": []},
					{"id": 1, "number": 2, "title": "归航", "duration": 1440, "bindings": [
						{"id": 1, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/a", "sourceLabel": "假弹幕源 a",
						 "title": "弹幕源 a", "duration": 1440, "offset": 1.5, "status": "active", "danmakuCount": 2, "seasonBindingId": null, "lastFetchedAt": null},
						{"id": 2, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/b", "sourceLabel": "假弹幕源 b",
						 "title": "弹幕源 b", "duration": 1380, "offset": 0, "status": "dead", "danmakuCount": 0, "seasonBindingId": null, "lastFetchedAt": null}
					]}
				]},
				{"id": 3, "number": 2, "title": "第 2 季", "seasonBindings": [], "episodes": []}
			]
		}`},
		{"/api/series/2", `{
			"id": 2, "type": "movie", "title": "长夜灯塔", "originalTitle": null, "year": 2020, "posterImageId": null,
			"seasons": [
				{"id": 4, "number": 1, "title": null, "seasonBindings": [], "episodes": [
					{"id": 4, "number": 1, "title": null, "duration": 5400, "bindings": [
						{"id": 4, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/d", "sourceLabel": "假弹幕源 d",
						 "title": "弹幕源 d", "duration": 5400, "offset": -2, "status": "active", "danmakuCount": 0, "seasonBindingId": null, "lastFetchedAt": null}
					]}
				]}
			]
		}`},
		{"/api/series/3", `{
			"id": 3, "type": "tv", "title": "空无一季", "originalTitle": null, "year": null, "posterImageId": null,
			"seasons": []
		}`},
	}
	for _, tt := range tests {
		_, _, data := call(t, srv, http.MethodGet, tt.target, "", http.StatusOK)
		assertJSON(t, data, tt.want)
	}

	for _, tt := range []struct {
		target      string
		wantStatus  int
		wantMessage string
	}{
		{"/api/series/4", http.StatusNotFound, "剧不存在"},
		{"/api/series/0", http.StatusBadRequest, "剧 ID 不合法"},
		{"/api/series/abc", http.StatusBadRequest, "请求参数错误"},
	} {
		if code, message, _ := call(t, srv, http.MethodGet, tt.target, "", tt.wantStatus); code != 1 || message != tt.wantMessage {
			t.Errorf("GET %s: code=%d message=%q, want %q", tt.target, code, message, tt.wantMessage)
		}
	}
}

// TestGetImage 图片接口是统一响应约定的例外：成功时直接返回原始字节；出错时照常是统一结构。
func TestGetImage(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	rec := serve(t, srv, http.MethodGet, "/api/images/1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want 永久缓存", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), posterPNG) {
		t.Errorf("body = %q, want 原始字节 %q", rec.Body.Bytes(), posterPNG)
	}

	for _, tt := range []struct {
		target      string
		wantStatus  int
		wantMessage string
	}{
		{"/api/images/2", http.StatusNotFound, "图片不存在"},
		{"/api/images/0", http.StatusBadRequest, "图片 ID 不合法"},
		{"/api/images/abc", http.StatusBadRequest, "请求参数错误"},
	} {
		if code, message, _ := call(t, srv, http.MethodGet, tt.target, "", tt.wantStatus); code != 1 || message != tt.wantMessage {
			t.Errorf("GET %s: code=%d message=%q, want %q", tt.target, code, message, tt.wantMessage)
		}
		if got := serve(t, srv, http.MethodGet, tt.target, "").Header().Get("Cache-Control"); got != "" {
			t.Errorf("GET %s: 出错时不应缓存，Cache-Control = %q", tt.target, got)
		}
	}
}

// TestDeleteCatalog 删除剧、季、集返回 200 和 null；删除剧时它的海报一起删除。级联删除的结果见 service 的测试。
func TestDeleteCatalog(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	// 集 1 在季 1 里，季 1 在剧 1 里：由下往上删
	for _, target := range []string{"/api/episodes/1", "/api/seasons/1", "/api/series/1"} {
		if code, _, data := call(t, srv, http.MethodDelete, target, "", http.StatusOK); code != 0 || string(data) != "null" {
			t.Errorf("DELETE %s: code=%d data=%s, want 0 null", target, code, data)
		}
	}
	for _, tt := range []struct {
		method      string
		target      string
		wantStatus  int
		wantMessage string
	}{
		{http.MethodGet, "/api/images/1", http.StatusNotFound, "图片不存在"}, // 剧 1 的海报
		{http.MethodDelete, "/api/series/1", http.StatusNotFound, "剧不存在"},
		{http.MethodDelete, "/api/seasons/1", http.StatusNotFound, "季不存在"},
		{http.MethodDelete, "/api/episodes/1", http.StatusNotFound, "集不存在"},
		{http.MethodDelete, "/api/series/0", http.StatusBadRequest, "剧 ID 不合法"},
		{http.MethodDelete, "/api/seasons/0", http.StatusBadRequest, "季 ID 不合法"},
		{http.MethodDelete, "/api/episodes/0", http.StatusBadRequest, "集 ID 不合法"},
		{http.MethodDelete, "/api/seasons/abc", http.StatusBadRequest, "请求参数错误"},
	} {
		if code, message, _ := call(t, srv, tt.method, tt.target, "", tt.wantStatus); code != 1 || message != tt.wantMessage {
			t.Errorf("%s %s: code=%d message=%q, want %q", tt.method, tt.target, code, message, tt.wantMessage)
		}
	}
	// 别的剧不受影响
	call(t, srv, http.MethodGet, "/api/series/2", "", http.StatusOK)
}

// assertJSON 按语义比较 JSON：字段名与值都要一致，不管字段顺序与空白。
func assertJSON(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("响应不是 JSON：%s", got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("期望值不是 JSON：%v", err)
	}
	if !reflect.DeepEqual(g, w) {
		// 两边都重新编码，字段按名称排序，方便对照
		t.Errorf("got  %s\nwant %s", jsonString(g), jsonString(w))
	}
}
