// 导出的 HTTP 测试。文件不叫 export_test.go：那个名字在仓库里约定给测试导出内部符号用。
package server

import (
	"log/slog"
	"mime"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/danmaku/bilifmt"
	exporthandler "github.com/kzw200015/danfuse/backend/internal/export/handler"
	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	"github.com/kzw200015/danfuse/backend/internal/infra/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// exportServer 起完整的 Echo，导出接口连到 pool。
func exportServer(pool *pgxpool.Pool) *Server {
	logger := slog.New(slog.DiscardHandler)
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
