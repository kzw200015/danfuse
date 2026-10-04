package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/handler"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// serve 用完整的 Echo（中间件、全局错误处理与路由）处理一个请求，返回记录下的响应。
func serve(t *testing.T, srv *Server, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, target, nil))
	return rec
}

func TestHealth(t *testing.T) {
	tests := []struct {
		name       string
		dbDown     bool
		wantStatus int
		wantBody   string
	}{
		{"数据库正常", false, http.StatusOK, `{"code":0,"message":"ok","data":{"status":"ok"}}`},
		{"数据库不可用", true, http.StatusServiceUnavailable, `{"code":1,"message":"数据库不可用","data":null}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pool := dbtest.Pool(t)
			if tt.dbDown {
				pool.Close()
			}
			srv := New(config.Server{}, slog.New(slog.DiscardHandler), &handler.Handlers{Health: handler.NewHealthHandler(pool)})

			rec := serve(t, srv, http.MethodGet, "/api/health")

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantBody {
				t.Errorf("body = %s, want %s", got, tt.wantBody)
			}
		})
	}
}
