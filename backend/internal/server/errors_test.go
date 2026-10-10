package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
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
		{"带底层原因的 5xx", fail(apierr.ErrBadGateway.WithMessage("上游超时").Wrap(cause)), "/items/42", http.StatusBadGateway, "上游超时: connection refused"},
		{"panic", func(*echo.Context) error { panic("boom") }, "/items/42", http.StatusInternalServerError, "boom"},
		{"4xx 不记录", fail(apierr.ErrNotFound.WithMessage("条目不存在")), "/items/42", http.StatusNotFound, ""},
		{"参数错误不记录", fail(apierr.ErrBadRequest), "/items/42", http.StatusBadRequest, ""},
		{"路由不存在不记录", fail(nil), "/api/missing", http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			srv := New(config.Server{}, config.Dandanplay{}, slog.New(slog.NewJSONHandler(&logs, nil)), &Handlers{Health: NewHealthHandler(nil)})
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

			entry := decodeLogEntry(t, &logs)
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

// TestErrorHandlerClientGone 客户端断开（请求的 ctx 已取消）时出的 5xx 不算服务端故障：记 info 级别的 "request canceled"。
func TestErrorHandlerClientGone(t *testing.T) {
	var logs bytes.Buffer
	srv := New(config.Server{}, config.Dandanplay{}, slog.New(slog.NewJSONHandler(&logs, nil)), &Handlers{Health: NewHealthHandler(nil)})
	srv.echo.GET("/items/:id", func(c *echo.Context) error {
		return fmt.Errorf("list items: %w", c.Request().Context().Err())
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/items/42", nil)

	srv.echo.ServeHTTP(httptest.NewRecorder(), req)

	if entry := decodeLogEntry(t, &logs); entry.Level != "INFO" || entry.Msg != "request canceled" || entry.Route != "/items/:id" || entry.Error != "list items: context canceled" {
		t.Errorf("日志 = %s, want INFO request canceled，路由与错误链照常记录", logs.String())
	}
}
