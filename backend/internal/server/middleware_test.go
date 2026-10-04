package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/handler"
	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
)

func TestErrorHandlerLogsServerErrors(t *testing.T) {
	cause := errors.New("connection refused")
	fail := func(err error) echo.HandlerFunc {
		return func(*echo.Context) error { return err }
	}

	tests := []struct {
		name       string
		handler    echo.HandlerFunc // 注册在 /items/:id 上
		target     string
		wantStatus int
		wantError  string // 日志里错误链应包含的内容，为空表示不记日志
	}{
		{"未知错误", fail(fmt.Errorf("list items: %w", cause)), "/items/42", http.StatusInternalServerError, "list items: connection refused"},
		{"带底层原因的 5xx", fail(errcode.ErrBadGateway.WithMessage("上游超时").Wrap(cause)), "/items/42", http.StatusBadGateway, "上游超时: connection refused"},
		{"panic", func(*echo.Context) error { panic("boom") }, "/items/42", http.StatusInternalServerError, "boom"},
		{"4xx 不记录", fail(errcode.ErrNotFound.WithMessage("条目不存在")), "/items/42", http.StatusNotFound, ""},
		{"参数错误不记录", fail(errcode.ErrBadRequest), "/items/42", http.StatusBadRequest, ""},
		{"路由不存在不记录", fail(nil), "/api/missing", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			srv := New(config.Server{}, config.Dandanplay{}, slog.New(slog.NewJSONHandler(&logs, nil)), &handler.Handlers{Health: handler.NewHealthHandler(nil)}, nil)
			srv.echo.GET("/items/:id", tt.handler)

			rec := serve(t, srv, http.MethodGet, tt.target, "")

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantError == "" {
				if logs.Len() != 0 {
					t.Errorf("不应记录日志，实际：%s", logs.String())
				}
				return
			}

			var entry struct {
				Level     string `json:"level"`
				RequestID string `json:"request_id"`
				Method    string `json:"method"`
				Route     string `json:"route"`
				Error     string `json:"error"`
			}
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatalf("应恰好记录一条日志：%v\n%s", err, logs.String())
			}
			if entry.Level != "ERROR" {
				t.Errorf("level = %q, want ERROR", entry.Level)
			}
			if want := rec.Header().Get(echo.HeaderXRequestID); entry.RequestID == "" || entry.RequestID != want {
				t.Errorf("request_id = %q, want %q", entry.RequestID, want)
			}
			if entry.Method != http.MethodGet {
				t.Errorf("method = %q, want GET", entry.Method)
			}
			if entry.Route != "/items/:id" {
				t.Errorf("route = %q, want /items/:id", entry.Route)
			}
			if !strings.Contains(entry.Error, tt.wantError) {
				t.Errorf("error = %q, want containing %q", entry.Error, tt.wantError)
			}
		})
	}
}
