package server

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/handler"
)

func TestSettings(t *testing.T) {
	const apiKey = "0123456789abcdef-api-key"
	tests := []struct {
		name       string
		dandanplay config.Dandanplay
		source     config.CatalogSource
		sync       config.Sync
		wantData   string
	}{
		{
			"未配置目录源",
			config.Dandanplay{},
			config.CatalogSource{},
			config.Sync{},
			`{"dandanplayToken":null,"catalogSource":null,"syncInterval":0}`,
		},
		{
			"kind 为空时不读 Jellyfin 的设置块",
			config.Dandanplay{},
			config.CatalogSource{Jellyfin: config.Jellyfin{
				URL: "http://192.168.1.10:8096", APIKey: apiKey, Libraries: []string{"番剧"},
			}},
			config.Sync{},
			`{"dandanplayToken":null,"catalogSource":null,"syncInterval":0}`,
		},
		{
			"Jellyfin",
			config.Dandanplay{Token: "s3cret"},
			config.CatalogSource{Kind: config.KindJellyfin, Jellyfin: config.Jellyfin{
				URL: "http://192.168.1.10:8096/jellyfin", APIKey: apiKey, Libraries: []string{"番剧", "电影"},
			}},
			config.Sync{Interval: 24 * time.Hour},
			`{"dandanplayToken":"s3cret","catalogSource":{"kind":"jellyfin","url":"http://192.168.1.10:8096/jellyfin","libraries":["番剧","电影"]},"syncInterval":86400}`,
		},
		{
			"定时间隔不是整秒",
			config.Dandanplay{},
			config.CatalogSource{Kind: config.KindJellyfin, Jellyfin: config.Jellyfin{
				URL: "http://jellyfin:8096", APIKey: apiKey, Libraries: []string{"番剧"},
			}},
			config.Sync{Interval: 1500 * time.Millisecond},
			`{"dandanplayToken":null,"catalogSource":{"kind":"jellyfin","url":"http://jellyfin:8096","libraries":["番剧"]},"syncInterval":1.5}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := New(config.Server{}, config.Dandanplay{}, slog.New(slog.DiscardHandler), &handler.Handlers{
				Settings: handler.NewSettingsHandler(tt.dandanplay, tt.source, tt.sync),
			}, nil)

			rec := serve(t, srv, http.MethodGet, "/api/settings")

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body %s", rec.Code, rec.Body)
			}
			body := rec.Body.String()
			if want := `{"code":0,"message":"ok","data":` + tt.wantData + `}`; strings.TrimSpace(body) != want {
				t.Errorf("body = %s\nwant %s", body, want)
			}
			if strings.Contains(body, apiKey) {
				t.Errorf("响应里出现了 API key：%s", body)
			}
		})
	}
}
