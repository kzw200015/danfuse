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

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/handler"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/service"
)

// gatedSource 假目录源：每产出一部剧之前等 gate 放行一次。
type gatedSource struct {
	items []catalog.Item
	gate  chan struct{}
}

func (s *gatedSource) List(ctx context.Context) (catalog.Listing, error) {
	return catalog.Listing{
		Total:    len(s.items),
		Warnings: []string{"找不到媒体库「动画」，已跳过"},
		Items: func(yield func(catalog.Item, error) bool) {
			for _, item := range s.items {
				select {
				case <-s.gate:
				case <-ctx.Done():
					yield(catalog.Item{}, ctx.Err())
					return
				}
				if !yield(item, nil) {
					return
				}
			}
		},
	}, nil
}

// syncServer 在 synctest 气泡里起 SyncService（后台运行 Run）和完整的 Echo。src 为 nil 表示未配置目录源。
// 连接池在气泡里创建、在气泡里关闭；synctest.Wait 等到后台的同步停下来。
func syncServer(t *testing.T, cfg *pgxpool.Config, src catalog.Source) *Server {
	t.Helper()
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	logger := slog.New(slog.DiscardHandler)
	svc := service.NewSyncService(repository.NewStore(pool), pool, src, config.Sync{}, logger)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); <-done }) // 在关闭连接池之前
	synctest.Wait()

	return New(config.Server{}, config.Dandanplay{}, logger, &handler.Handlers{
		Health: handler.NewHealthHandler(pool),
		Sync:   handler.NewSyncHandler(svc),
	}, nil)
}

// call 发一个请求，检查状态码，返回解出的统一响应。
func call(t *testing.T, srv *Server, method, target string, wantStatus int) (code int, message string, data json.RawMessage) {
	t.Helper()
	rec := serve(t, srv, method, target)
	if rec.Code != wantStatus {
		t.Fatalf("%s %s: status = %d, want %d, body %s", method, target, rec.Code, wantStatus, rec.Body)
	}
	var resp struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s %s: 不是统一响应：%s", method, target, rec.Body)
	}
	return resp.Code, resp.Message, resp.Data
}

func TestSyncRunsAPI(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		src := &gatedSource{
			items: []catalog.Item{
				{Name: "甲", Series: &catalog.Series{Type: catalog.TypeTV, Title: "甲", Seasons: []catalog.Season{
					{Number: 1, Episodes: []catalog.Episode{{Number: 1}, {Number: 2}}},
				}}},
				{Name: "乙", Warnings: []string{"没有有效的集，整部跳过"}},
			},
			gate: make(chan struct{}),
		}
		srv := syncServer(t, cfg, src)

		// 触发立即返回 202 和同步 ID，同步在后台进行
		if _, _, data := call(t, srv, http.MethodPost, "/api/sync-runs", http.StatusAccepted); string(data) != `{"id":1}` {
			t.Errorf("触发返回 %s, want {\"id\":1}", data)
		}
		synctest.Wait()

		code, message, _ := call(t, srv, http.MethodPost, "/api/sync-runs", http.StatusConflict)
		if code != 1 || message != "同步正在进行" {
			t.Errorf("同步进行中再触发：code=%d message=%q", code, message)
		}

		// 进行中每提交一部剧，已完成数加一
		progress := func() string {
			t.Helper()
			var run struct {
				Status string `json:"status"`
				Total  *int   `json:"total"`
				Done   int    `json:"done"`
			}
			_, _, data := call(t, srv, http.MethodGet, "/api/sync-runs/1", http.StatusOK)
			if err := json.Unmarshal(data, &run); err != nil {
				t.Fatal(err)
			}
			return strings.Join([]string{run.Status, jsonString(run.Total), jsonString(run.Done)}, " ")
		}
		for i, want := range []string{"running 2 0", "running 2 1", "succeeded 2 2"} {
			if i > 0 {
				src.gate <- struct{}{}
				synctest.Wait()
			}
			if got := progress(); got != want {
				t.Errorf("放行 %d 部后：%s, want %s", i, got, want)
			}
		}

		// 详情含警告，列表不含
		_, _, data := call(t, srv, http.MethodGet, "/api/sync-runs/1", http.StatusOK)
		detail := decodeObject(t, data)
		wantFields := []string{
			"createdEpisodes", "createdSeasons", "createdSeries", "done", "error", "finishedAt", "id",
			"startedAt", "status", "total", "trigger", "warningCount", "warnings",
		}
		if got := slices.Sorted(maps.Keys(detail)); !slices.Equal(got, wantFields) {
			t.Errorf("详情的字段 = %q\nwant %q", got, wantFields)
		}
		wantDetail := map[string]string{
			"id": `1`, "trigger": `"manual"`, "createdSeries": `1`, "createdSeasons": `1`, "createdEpisodes": `2`,
			"warningCount": `2`, "warnings": `["找不到媒体库「动画」，已跳过","乙：没有有效的集，整部跳过"]`, "error": `null`,
		}
		for k, want := range wantDetail {
			if got := string(detail[k]); got != want {
				t.Errorf("详情 %s = %s, want %s", k, got, want)
			}
		}

		_, _, data = call(t, srv, http.MethodGet, "/api/sync-runs", http.StatusOK)
		var list []map[string]json.RawMessage
		if err := json.Unmarshal(data, &list); err != nil {
			t.Fatal(err)
		}
		wantListFields := slices.DeleteFunc(slices.Clone(wantFields), func(f string) bool { return f == "warnings" })
		if len(list) != 1 || !slices.Equal(slices.Sorted(maps.Keys(list[0])), wantListFields) {
			t.Errorf("列表 = %s, want 一条不含 warnings 的记录", data)
		}

		for _, tt := range []struct {
			target      string
			wantStatus  int
			wantMessage string
		}{
			{"/api/sync-runs/2", http.StatusNotFound, "同步记录不存在"},
			{"/api/sync-runs/0", http.StatusBadRequest, "同步记录 ID 不合法"},
			{"/api/sync-runs/abc", http.StatusBadRequest, "请求参数错误"},
		} {
			if code, message, _ := call(t, srv, http.MethodGet, tt.target, tt.wantStatus); code != 1 || message != tt.wantMessage {
				t.Errorf("GET %s: code=%d message=%q, want %q", tt.target, code, message, tt.wantMessage)
			}
		}
	})
}

func TestTriggerSyncWithoutCatalogSource(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		srv := syncServer(t, cfg, nil)

		code, message, _ := call(t, srv, http.MethodPost, "/api/sync-runs", http.StatusConflict)
		if code != 1 || message != "未配置目录源" {
			t.Errorf("code=%d message=%q, want 未配置目录源", code, message)
		}
		if _, _, data := call(t, srv, http.MethodGet, "/api/sync-runs", http.StatusOK); string(data) != "[]" {
			t.Errorf("没有同步记录时列表 = %s, want []", data)
		}
	})
}

func decodeObject(t *testing.T, data json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	return obj
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
