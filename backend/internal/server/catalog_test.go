package server

import (
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
)

// catalogServer 起完整的 Echo，目录接口连到 pool。
func catalogServer(pool *pgxpool.Pool) *Server {
	svc := service.NewCatalogService(repository.NewStore(pool))
	return New(config.Server{}, slog.New(slog.DiscardHandler), &handler.Handlers{
		Catalog: handler.NewCatalogHandler(svc),
	})
}

// seedCatalog 写入一个小目录。每个测试的库都从模板新建，ID 从 1 开始，按插入顺序分配（注释里标出）。
// 季和集故意不按编号顺序插入，用来检查接口按编号排序，而不是按 ID。
func seedCatalog(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO series (type, title, original_title, year) VALUES
			('tv', '星海旅人', 'Star Voyager', 2019), -- 剧 1
			('movie', '长夜灯塔', NULL, 2020),        -- 剧 2
			('tv', '空无一季', NULL, NULL);           -- 剧 3：没有季
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
	`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestListSeries(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	srv := catalogServer(pool)

	if _, _, data := call(t, srv, http.MethodGet, "/api/series", http.StatusOK); string(data) != "[]" {
		t.Errorf("目录为空时 = %s, want []", data)
	}

	seedCatalog(t, pool)
	_, _, data := call(t, srv, http.MethodGet, "/api/series", http.StatusOK)
	assertJSON(t, data, `[
		{"id": 1, "type": "tv", "title": "星海旅人", "originalTitle": "Star Voyager", "year": 2019,
		 "seasonCount": 3, "episodeCount": 3},
		{"id": 2, "type": "movie", "title": "长夜灯塔", "originalTitle": null, "year": 2020,
		 "seasonCount": 1, "episodeCount": 1},
		{"id": 3, "type": "tv", "title": "空无一季", "originalTitle": null, "year": null,
		 "seasonCount": 0, "episodeCount": 0}
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
		// 季按季号、集按集号排序；没有集的季、没有季的剧输出空数组
		{"/api/series/1", `{
			"id": 1, "type": "tv", "title": "星海旅人", "originalTitle": "Star Voyager", "year": 2019,
			"seasons": [
				{"id": 2, "number": 0, "title": null, "episodes": [
					{"id": 3, "number": 1, "title": null, "duration": null}
				]},
				{"id": 1, "number": 1, "title": "第 1 季", "episodes": [
					{"id": 2, "number": 1, "title": "启程", "duration": 1420},
					{"id": 1, "number": 2, "title": "归航", "duration": 1440}
				]},
				{"id": 3, "number": 2, "title": "第 2 季", "episodes": []}
			]
		}`},
		{"/api/series/2", `{
			"id": 2, "type": "movie", "title": "长夜灯塔", "originalTitle": null, "year": 2020,
			"seasons": [
				{"id": 4, "number": 1, "title": null, "episodes": [
					{"id": 4, "number": 1, "title": null, "duration": 5400}
				]}
			]
		}`},
		{"/api/series/3", `{
			"id": 3, "type": "tv", "title": "空无一季", "originalTitle": null, "year": null, "seasons": []
		}`},
	}
	for _, tt := range tests {
		_, _, data := call(t, srv, http.MethodGet, tt.target, http.StatusOK)
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
		if code, message, _ := call(t, srv, http.MethodGet, tt.target, tt.wantStatus); code != 1 || message != tt.wantMessage {
			t.Errorf("GET %s: code=%d message=%q, want %q", tt.target, code, message, tt.wantMessage)
		}
	}
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
