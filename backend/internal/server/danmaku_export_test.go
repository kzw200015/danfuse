// 导出的 HTTP 测试。文件不叫 export_test.go：那个名字在仓库里约定给测试导出内部符号用。
package server

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/danmaku/bilifmt"
	exporthandler "github.com/kzw200015/danfuse/backend/internal/export/handler"
	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	"github.com/kzw200015/danfuse/backend/internal/infra/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/infra/testutil"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// exportServer 起完整的 Echo，导出接口连到 pool，不记日志。
func exportServer(pool *pgxpool.Pool) *Server {
	return exportServerLogging(pool, slog.New(slog.DiscardHandler))
}

// exportServerLogging 同 exportServer，日志记到 logger。
func exportServerLogging(pool *pgxpool.Pool, logger *slog.Logger) *Server {
	env := testenv.New(pool, logger)
	return New(config.Server{}, logger, complete(&Handlers{Export: exporthandler.NewHandler(env.Export)}))
}

// TestExportBinding GET /api/bindings/:id/export：直接下载 XML，Content-Disposition 为 attachment，
// 同时给出 ASCII 的 filename 兜底（非 ASCII 字符换成 _）与 RFC 5987 的 filename*（UTF-8，百分号编码）。
func TestExportBinding(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	if _, err := pool.Exec(t.Context(), `UPDATE bindings SET title = 'Tom''s (v2)' WHERE id = 4`); err != nil {
		t.Fatal(err)
	}
	srv := exportServer(pool)

	tests := []struct {
		target, wantDisposition, wantFilename string
		wantCount                             int
	}{
		{
			"/api/bindings/1/export",
			`attachment; filename="___ a.xml"; filename*=UTF-8''%E5%BC%B9%E5%B9%95%E6%BA%90%20a.xml`, "弹幕源 a.xml", 2,
		},
		{
			"/api/bindings/4/export",
			`attachment; filename="Tom's (v2).xml"; filename*=UTF-8''Tom%27s%20%28v2%29.xml`, "Tom's (v2).xml", 0,
		},
	}
	for _, tt := range tests {
		rec := serve(t, srv, http.MethodGet, tt.target, "")

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200, body %s", tt.target, rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/xml" {
			t.Errorf("%s: Content-Type = %q, want application/xml", tt.target, ct)
		}
		disposition := rec.Header().Get("Content-Disposition")
		if disposition != tt.wantDisposition {
			t.Errorf("%s: Content-Disposition = %s\nwant %s", tt.target, disposition, tt.wantDisposition)
		}
		if _, params, err := mime.ParseMediaType(disposition); err != nil || params["filename"] != tt.wantFilename {
			t.Errorf("%s: 解出的文件名 = %q（%v）, want %q", tt.target, params["filename"], err, tt.wantFilename)
		}
		items, err := bilifmt.Decode(rec.Body.Bytes())
		if err != nil || len(items) != tt.wantCount {
			t.Errorf("%s: 读回 %d 条弹幕（%v）, want %d\n%s", tt.target, len(items), err, tt.wantCount, rec.Body)
		}
	}
}

func TestExportBindingErrors(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	assertAPIErrors(t, exportServer(pool), []apiError{
		{http.MethodGet, "/api/bindings/0/export", "", http.StatusBadRequest, "绑定 ID 不合法"},
		{http.MethodGet, "/api/bindings/abc/export", "", http.StatusBadRequest, "请求参数错误"},
		{http.MethodGet, "/api/bindings/99/export", "", http.StatusNotFound, "绑定不存在"},
	})
}

// TestExportEpisode GET /api/episodes/:id/export：直接下载 zip，Content-Disposition 同单绑定的导出；
// zip 里是有弹幕的绑定（集 1 的绑定 2 没有弹幕，跳过）。
func TestExportEpisode(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)

	rec := serve(t, exportServer(pool), http.MethodGet, "/api/episodes/1/export", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	wantDisposition := `attachment; filename="____ S01E02.zip"; filename*=UTF-8''%E6%98%9F%E6%B5%B7%E6%97%85%E4%BA%BA%20S01E02.zip`
	if disposition := rec.Header().Get("Content-Disposition"); disposition != wantDisposition {
		t.Errorf("Content-Disposition = %s\nwant %s", disposition, wantDisposition)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("zip 解不开：%v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	testutil.AssertStrings(t, "zip 里的文件", names, []string{"弹幕源 a.xml"})
}

func TestExportEpisodeErrors(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	assertAPIErrors(t, exportServer(pool), []apiError{
		{http.MethodGet, "/api/episodes/0/export", "", http.StatusBadRequest, "集 ID 不合法"},
		{http.MethodGet, "/api/episodes/abc/export", "", http.StatusBadRequest, "请求参数错误"},
		{http.MethodGet, "/api/episodes/99/export", "", http.StatusNotFound, "集不存在"},
		{http.MethodGet, "/api/episodes/2/export", "", http.StatusNotFound, "这一集没有可导出的弹幕"},
	})
}

// TestExportSeason GET /api/seasons/:id/export：直接下载 zip，Content-Disposition 同单绑定的导出；
// zip 里一层根目录 <剧名> S01/，每集一个 S01E02/ 子目录（集 2 没有绑定，不建子目录）。
func TestExportSeason(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)

	rec := serve(t, exportServer(pool), http.MethodGet, "/api/seasons/1/export", "")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
	wantDisposition := `attachment; filename="____ S01.zip"; filename*=UTF-8''%E6%98%9F%E6%B5%B7%E6%97%85%E4%BA%BA%20S01.zip`
	if disposition := rec.Header().Get("Content-Disposition"); disposition != wantDisposition {
		t.Errorf("Content-Disposition = %s\nwant %s", disposition, wantDisposition)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("zip 解不开：%v", err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	testutil.AssertStrings(t, "zip 里的文件", names, []string{"星海旅人 S01/S01E02/弹幕源 a.xml"})
}

// breakingTracer 第 n 次执行名为 query 的 sqlc 查询时，在发出之前关掉这条数据库连接，让它因网络错误失败。
type breakingTracer struct {
	query string
	n     int32
	count atomic.Int32
}

func (b *breakingTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "-- name: "+b.query+" ") && b.count.Add(1) == b.n {
		_ = conn.PgConn().Conn().Close()
	}
	return ctx
}

func (*breakingTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// TestExportSeasonAbort 响应头写出之后出错时中止连接：客户端拿到 200 和一部分 zip，读响应体时连接断开（缺了分块传输的结尾），
// 收到的 zip 没有中央目录、解不开；服务端记了一条 5xx 日志。
// 季 1 先导出第 1 集（集 2），它的弹幕多到超过各层写缓冲，响应头和一部分 zip 已经发出；
// 再读第 2 集（集 1）的绑定 1 的弹幕时数据库连接断开。
func TestExportSeasonAbort(t *testing.T) {
	t.Parallel()
	cfg := dbtest.Config(t)
	cfg.ConnConfig.Tracer = &breakingTracer{query: "ListDanmakuByBinding", n: 2}
	pool := dbtest.Open(t, cfg)
	seedCatalog(t, pool)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO bindings (episode_id, adapter, ref, title, duration, danmaku_count, max_time_ms) VALUES
			(2, 'fake', '{"name": "e"}', '弹幕源 e', 1420, 3000, 3000); -- 绑定 5
		INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text)
			SELECT 5, g, g, 1, 0, md5(g::text) FROM generate_series(1, 3000) g;`)
	if err != nil {
		t.Fatal(err)
	}
	var logs testenv.LockedBuffer
	ts := httptest.NewServer(exportServerLogging(pool, slog.New(slog.NewJSONHandler(&logs, nil))).echo)
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/api/seasons/1/export", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/zip" {
		t.Errorf("响应 = %d %s, want 200 application/zip", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("读响应体的错误 = %v, want %v（连接中止）", err, io.ErrUnexpectedEOF)
	}
	if len(body) == 0 {
		t.Error("响应体为空，want 已经发出一部分 zip")
	}
	if _, err := zip.NewReader(bytes.NewReader(body), int64(len(body))); err == nil {
		t.Error("收到的 zip 解得开，want 没有中央目录、不完整")
	}
	entry := decodeLogEntry(t, bytes.NewBufferString(logs.String()))
	if entry.Level != "ERROR" || entry.Msg != "request failed" || entry.Method != http.MethodGet || entry.Route != "/api/seasons/:id/export" ||
		entry.RequestID == "" || !strings.Contains(entry.Error, "export season 1: list danmaku of binding 1: ") {
		t.Errorf("日志 = %+v", entry)
	}
}

func TestExportSeasonErrors(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	assertAPIErrors(t, exportServer(pool), []apiError{
		{http.MethodGet, "/api/seasons/0/export", "", http.StatusBadRequest, "季 ID 不合法"},
		{http.MethodGet, "/api/seasons/abc/export", "", http.StatusBadRequest, "请求参数错误"},
		{http.MethodGet, "/api/seasons/99/export", "", http.StatusNotFound, "季不存在"},
		{http.MethodGet, "/api/seasons/2/export", "", http.StatusNotFound, "这一季没有可导出的弹幕"},
	})
}
