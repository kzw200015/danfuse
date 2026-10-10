package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// serve 用完整的 Echo（中间件、全局错误处理与路由）处理一个请求，返回记录下的响应。body 不为空时作为 JSON 请求体。
func serve(t *testing.T, srv *Server, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	}
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)
	return rec
}

// call 用 serve 处理一个请求，检查状态码，返回解出的统一响应。
func call(t *testing.T, srv *Server, method, target, body string, wantStatus int) (code int, message string, data json.RawMessage) {
	t.Helper()
	return decodeResponse(t, method+" "+target+" "+body, serve(t, srv, method, target, body), wantStatus)
}

// decodeResponse 检查状态码，返回解出的统一响应；what 写进失败信息，说明是哪个请求。
func decodeResponse(t *testing.T, what string, rec *httptest.ResponseRecorder, wantStatus int) (code int, message string, data json.RawMessage) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("%s: status = %d, want %d, body %s", what, rec.Code, wantStatus, rec.Body)
	}
	var resp struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%s: 不是统一响应：%s", what, rec.Body)
	}
	return resp.Code, resp.Message, resp.Data
}

// apiError 一个应当失败的管理 API 请求：期望的状态码和提示，业务码一律为 1。
type apiError struct {
	method, target, body string
	wantStatus           int
	wantMessage          string
}

// assertAPIErrors 逐个发出 cases 里的请求，检查状态码、业务码和提示。
func assertAPIErrors(t *testing.T, srv *Server, cases []apiError) {
	t.Helper()
	for _, tt := range cases {
		if code, message, _ := call(t, srv, tt.method, tt.target, tt.body, tt.wantStatus); code != 1 || message != tt.wantMessage {
			t.Errorf("%s %s %s: code=%d message=%q, want %q", tt.method, tt.target, tt.body, code, message, tt.wantMessage)
		}
	}
}

// logEntry 一条 JSON 日志里 logger.ServerError 写的字段。
type logEntry struct {
	Level     string `json:"level"`
	Msg       string `json:"msg"`
	RequestID string `json:"request_id"`
	Method    string `json:"method"`
	Route     string `json:"route"`
	Error     string `json:"error"`
}

// decodeLogEntry 解出 logs 里恰好一条的 JSON 日志。
func decodeLogEntry(t *testing.T, logs *bytes.Buffer) logEntry {
	t.Helper()
	var entry logEntry
	if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
		t.Fatalf("应恰好记录一条日志：%v\n%s", err, logs.String())
	}
	return entry
}

// decodeObject 把 JSON 对象解成字段名到原始值的映射。
func decodeObject(t *testing.T, data json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	return obj
}

// popTime 检查 obj 里有 key 且不为 null（取决于当前时间的字段），再把它删掉，方便与固定的 JSON 比较其余字段。
func popTime(t *testing.T, obj map[string]json.RawMessage, key string) {
	t.Helper()
	if v, ok := obj[key]; !ok || string(v) == "null" {
		t.Errorf("%s = %s，want 一个时间", key, v)
	}
	delete(obj, key)
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// startSync 在 synctest 气泡里新建连接池和不开定时同步的 SyncService，在后台运行 Run，等它停下来再返回。
// src 为 nil 表示未配置目录源。连接池在气泡里创建、在气泡里关闭：测试结束时先取消 Run 并等它返回，再关闭连接池。
func startSync(t *testing.T, cfg *pgxpool.Config, src catalog.Source) (*catalog.SyncService, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.Open(t, cfg)
	svc := catalog.NewSyncService(pool, src, config.Sync{KeepRuns: 20}, slog.New(slog.DiscardHandler))
	testenv.RunInBackground(t, svc)
	return svc, pool
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
			srv := New(config.Server{}, config.Dandanplay{}, slog.New(slog.DiscardHandler), &Handlers{Health: NewHealthHandler(pool)})

			rec := serve(t, srv, http.MethodGet, "/api/health", "")

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantBody {
				t.Errorf("body = %s, want %s", got, tt.wantBody)
			}
		})
	}
}
