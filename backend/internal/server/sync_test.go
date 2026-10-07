package server

import (
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/handler"
)

// syncServer 在 synctest 气泡里起 SyncService（startSync）和完整的 Echo。src 为 nil 表示未配置目录源。
func syncServer(t *testing.T, cfg *pgxpool.Config, src catalog.Source) *Server {
	t.Helper()
	svc, pool := startSync(t, cfg, src)
	return New(config.Server{}, config.Dandanplay{}, slog.New(slog.DiscardHandler), &handler.Handlers{
		Health: handler.NewHealthHandler(pool),
		Sync:   handler.NewSyncHandler(svc),
	}, nil)
}

func TestSyncRunsAPI(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		src := &fakeCatalog{
			items: []catalog.Item{
				{Name: "甲", Series: &catalog.Series{Type: catalog.TypeTV, Title: "甲", Seasons: []catalog.Season{
					{Number: 1, Episodes: []catalog.Episode{{Number: 1}, {Number: 2}}},
				}}},
				{Name: "乙", Warnings: []string{"没有有效的集，整部跳过"}},
			},
			warnings: []string{"找不到媒体库「动画」，已跳过"},
			gate:     make(chan struct{}),
		}
		srv := syncServer(t, cfg, src)

		// 一次都没同步过时最近一次为 null
		if _, _, data := call(t, srv, http.MethodGet, "/api/sync-runs/latest", "", http.StatusOK); string(data) != "null" {
			t.Errorf("没有同步记录时最近一次 = %s, want null", data)
		}

		// 触发立即返回 202 和同步 ID，同步在后台进行
		if _, _, data := call(t, srv, http.MethodPost, "/api/sync-runs", "", http.StatusAccepted); string(data) != `{"id":1}` {
			t.Errorf("触发返回 %s, want {\"id\":1}", data)
		}
		synctest.Wait()

		code, message, _ := call(t, srv, http.MethodPost, "/api/sync-runs", "", http.StatusConflict)
		if code != 1 || message != "同步正在进行" {
			t.Errorf("同步进行中再触发：code=%d message=%q", code, message)
		}

		// 进度与警告怎样随同步写入见 service 的 TestSyncProgress；这里放行两部剧，让同步结束
		for range 2 {
			src.gate <- struct{}{}
		}
		synctest.Wait()

		// 详情含警告，列表不含
		_, _, data := call(t, srv, http.MethodGet, "/api/sync-runs/1", "", http.StatusOK)
		detail := decodeObject(t, data)
		wantFields := []string{
			"createdEpisodes", "createdSeasons", "createdSeries", "done", "error", "finishedAt", "id",
			"startedAt", "status", "total", "trigger", "warningCount", "warnings",
		}
		if got := slices.Sorted(maps.Keys(detail)); !slices.Equal(got, wantFields) {
			t.Errorf("详情的字段 = %q\nwant %q", got, wantFields)
		}
		wantDetail := map[string]string{
			"id": `1`, "trigger": `"manual"`, "status": `"succeeded"`, "total": `2`, "done": `2`, "createdSeries": `1`, "createdSeasons": `1`, "createdEpisodes": `2`,
			"warningCount": `2`, "warnings": `["找不到媒体库「动画」，已跳过","乙：没有有效的集，整部跳过"]`, "error": `null`,
		}
		for k, want := range wantDetail {
			if got := string(detail[k]); got != want {
				t.Errorf("详情 %s = %s, want %s", k, got, want)
			}
		}

		_, _, data = call(t, srv, http.MethodGet, "/api/sync-runs", "", http.StatusOK)
		var list []map[string]json.RawMessage
		if err := json.Unmarshal(data, &list); err != nil {
			t.Fatal(err)
		}
		wantListFields := slices.DeleteFunc(slices.Clone(wantFields), func(f string) bool { return f == "warnings" })
		if len(list) != 1 || !slices.Equal(slices.Sorted(maps.Keys(list[0])), wantListFields) {
			t.Errorf("列表 = %s, want 一条不含 warnings 的记录", data)
		}

		// 最近一次与列表的第一条相同
		_, _, latest := call(t, srv, http.MethodGet, "/api/sync-runs/latest", "", http.StatusOK)
		if !maps.EqualFunc(decodeObject(t, latest), list[0], func(a, b json.RawMessage) bool { return string(a) == string(b) }) {
			t.Errorf("最近一次 = %s\nwant 列表的第一条 %s", latest, data)
		}

		assertAPIErrors(t, srv, []apiError{
			{http.MethodGet, "/api/sync-runs/2", "", http.StatusNotFound, "同步记录不存在"},
			{http.MethodGet, "/api/sync-runs/0", "", http.StatusBadRequest, "同步记录 ID 不合法"},
			{http.MethodGet, "/api/sync-runs/abc", "", http.StatusBadRequest, "请求参数错误"},
		})
	})
}

func TestTriggerSyncWithoutCatalogSource(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv := syncServer(t, cfg, nil)

		code, message, _ := call(t, srv, http.MethodPost, "/api/sync-runs", "", http.StatusConflict)
		if code != 1 || message != "未配置目录源" {
			t.Errorf("code=%d message=%q, want 未配置目录源", code, message)
		}
		if _, _, data := call(t, srv, http.MethodGet, "/api/sync-runs", "", http.StatusOK); string(data) != "[]" {
			t.Errorf("没有同步记录时列表 = %s, want []", data)
		}
	})
}
