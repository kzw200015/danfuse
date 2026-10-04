package server

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/handler"
	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
)

func TestFrontend(t *testing.T) {
	const (
		indexHTML = `<!doctype html><title>Danfuse</title>`
		script    = `console.log("danfuse")`
		notFound  = `{"code":1,"message":"Not Found","data":null}`
		immutable = "public, max-age=31536000, immutable"
	)
	files := fstest.MapFS{
		"index.html":          {Data: []byte(indexHTML)},
		"favicon.svg":         {Data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)},
		"assets/index-abc.js": {Data: []byte(script)},
	}

	tests := []struct {
		name             string
		target           string
		wantStatus       int
		wantBody         string
		wantCacheControl string
	}{
		{"首页", "/", http.StatusOK, indexHTML, "no-cache"},
		{"直接刷新剧", "/catalog/1", http.StatusOK, indexHTML, "no-cache"},
		{"直接刷新集", "/catalog/1/2/3", http.StatusOK, indexHTML, "no-cache"},
		{"直接刷新同步页", "/sync?run=3", http.StatusOK, indexHTML, "no-cache"},
		{"根目录下的文件", "/favicon.svg", http.StatusOK, `<svg xmlns="http://www.w3.org/2000/svg"/>`, "no-cache"},
		{"带哈希的资源", "/assets/index-abc.js", http.StatusOK, script, immutable},
		{"不存在的资源不回退到 index.html", "/assets/index-old.js", http.StatusNotFound, notFound, ""},
		{"管理 API", "/api/items", http.StatusOK, `{"code":0,"message":"ok","data":null}`, ""},
		{"不存在的管理 API", "/api/%E4%B8%8D%E5%AD%98%E5%9C%A8", http.StatusNotFound, notFound, ""},
		{"/api 本身", "/api", http.StatusNotFound, notFound, ""},
		{"弹弹 API 下不存在的路径", "/dandanplay/api/v2/match", http.StatusNotFound, notFound, ""},
		{"带 token 的弹弹 API 下不存在的路径", "/dandanplay/token/api/v2/bangumi/1", http.StatusNotFound, notFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := newServer(config.Server{}, slog.New(slog.DiscardHandler), &handler.Handlers{}, files)
			srv.echo.GET("/api/items", func(c *echo.Context) error { return response.OK(c, nil) })

			rec := serve(t, srv, http.MethodGet, tt.target)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantBody {
				t.Errorf("body = %s, want %s", got, tt.wantBody)
			}
			if got := rec.Header().Get(echo.HeaderCacheControl); got != tt.wantCacheControl {
				t.Errorf("Cache-Control = %q, want %q", got, tt.wantCacheControl)
			}
		})
	}
}
