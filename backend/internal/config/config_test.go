package config

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

// setEnv 设置一组环境变量（键不带 DANFUSE_ 前缀），测试结束后自动还原。
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(EnvPrefix+"_"+k, v)
	}
}

func TestLoadCatalogSourceFromEnv(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_DSN":                      "postgres://localhost/danfuse",
		"CATALOG_SOURCE_KIND":               "jellyfin",
		"CATALOG_SOURCE_JELLYFIN_URL":       "http://192.168.1.10:8096/jellyfin/",
		"CATALOG_SOURCE_JELLYFIN_API_KEY":   "secret",
		"CATALOG_SOURCE_JELLYFIN_LIBRARIES": "番剧, 电影,,",
		"SYNC_INTERVAL":                     "24h",
	})

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	jf := cfg.CatalogSource.Jellyfin
	if cfg.CatalogSource.Kind != KindJellyfin {
		t.Errorf("kind = %q, want jellyfin", cfg.CatalogSource.Kind)
	}
	if jf.URL != "http://192.168.1.10:8096/jellyfin" {
		t.Errorf("url = %q, want trailing / removed", jf.URL)
	}
	if jf.APIKey != "secret" {
		t.Errorf("api_key = %q, want secret", jf.APIKey)
	}
	if want := []string{"番剧", "电影"}; !slices.Equal(jf.Libraries, want) {
		t.Errorf("libraries = %q, want %q", jf.Libraries, want)
	}
	if cfg.Sync.Interval != 24*time.Hour {
		t.Errorf("interval = %v, want 24h", cfg.Sync.Interval)
	}
}

func TestLoadDefaultsWithoutCatalogSource(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_DSN": "postgres://localhost/danfuse"})

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CatalogSource.Kind != "" || cfg.Sync.Interval != 0 {
		t.Errorf("默认应不启用目录源与定时同步，实际 kind=%q interval=%v", cfg.CatalogSource.Kind, cfg.Sync.Interval)
	}
}

func TestLoadAcceptsMaxConns(t *testing.T) {
	for _, maxConns := range []string{"0", "2"} { // 0 表示用 pgx 的默认值
		t.Run(maxConns, func(t *testing.T) {
			setEnv(t, map[string]string{"DATABASE_DSN": "postgres://localhost/danfuse", "DATABASE_MAX_CONNS": maxConns})
			if _, err := Load(""); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	jellyfin := map[string]string{
		"CATALOG_SOURCE_KIND":               "jellyfin",
		"CATALOG_SOURCE_JELLYFIN_URL":       "http://jellyfin:8096",
		"CATALOG_SOURCE_JELLYFIN_API_KEY":   "secret",
		"CATALOG_SOURCE_JELLYFIN_LIBRARIES": "番剧",
	}
	with := func(base map[string]string, k, v string) map[string]string {
		env := maps.Clone(base)
		env[k] = v
		return env
	}

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{"kind 不支持", map[string]string{"CATALOG_SOURCE_KIND": "plex"}, "catalog_source.kind"},
		{"缺 url", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_URL", ""), "jellyfin.url"},
		{"url 不是 http(s)", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_URL", "ftp://jellyfin"), "jellyfin.url"},
		{"url 没有主机", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_URL", "http://"), "jellyfin.url"},
		{"url 带查询串", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_URL", "http://jellyfin:8096/?api_key=secret"), "query or fragment"},
		{"url 带空查询串", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_URL", "http://jellyfin:8096?"), "query or fragment"},
		{"url 带片段", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_URL", "http://jellyfin:8096/#web"), "query or fragment"},
		{"缺 api_key", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_API_KEY", ""), "jellyfin.api_key"},
		{"缺 libraries", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_LIBRARIES", ""), "jellyfin.libraries"},
		{"libraries 只有空白", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_LIBRARIES", " , "), "jellyfin.libraries"},
		{"interval 为负", with(jellyfin, "SYNC_INTERVAL", "-1h"), "sync.interval"},
		{"interval > 0 但没有目录源", map[string]string{"SYNC_INTERVAL": "1h"}, "sync.interval"},
		{"max_conns 为 1", map[string]string{"DATABASE_MAX_CONNS": "1"}, "database.max_conns"},
		{"max_conns 为负", map[string]string{"DATABASE_MAX_CONNS": "-1"}, "database.max_conns"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, with(tt.env, "DATABASE_DSN", "postgres://localhost/danfuse"))

			_, err := Load("")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
