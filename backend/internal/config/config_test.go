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
		t.Setenv(envPrefix+"_"+k, v)
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

func TestLoadTunableDefaults(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_DSN": "postgres://localhost/danfuse"})

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.CatalogSource.Jellyfin.ListTimeout; got != 2*time.Minute {
		t.Errorf("list_timeout = %v, want 2m", got)
	}
	if cfg.Sync.KeepRuns != 20 {
		t.Errorf("keep_runs = %d, want 20", cfg.Sync.KeepRuns)
	}
	if cfg.Bilibili.RequestsPerSecond != 3 {
		t.Errorf("requests_per_second = %v, want 3", cfg.Bilibili.RequestsPerSecond)
	}
	if want := (DanmakuFile{MaxFiles: 50, MaxFileMB: 10, MaxUploadMB: 50}); cfg.DanmakuFile != want {
		t.Errorf("danmaku_file = %+v, want %+v", cfg.DanmakuFile, want)
	}
}

func TestLoadTunablesFromEnv(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_DSN":                         "postgres://localhost/danfuse",
		"SERVER_WRITE_TIMEOUT":                 "0", // 不限
		"CATALOG_SOURCE_JELLYFIN_LIST_TIMEOUT": "10m",
		"SYNC_KEEP_RUNS":                       "1",
		"BILIBILI_REQUESTS_PER_SECOND":         "0.5",
		"DANMAKU_FILE_MAX_FILES":               "200",
		"DANMAKU_FILE_MAX_FILE_MB":             "64",
		"DANMAKU_FILE_MAX_UPLOAD_MB":           "256",
	})

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.WriteTimeout != 0 {
		t.Errorf("write_timeout = %v, want 0", cfg.Server.WriteTimeout)
	}
	if got := cfg.CatalogSource.Jellyfin.ListTimeout; got != 10*time.Minute {
		t.Errorf("list_timeout = %v, want 10m", got)
	}
	if cfg.Sync.KeepRuns != 1 {
		t.Errorf("keep_runs = %d, want 1", cfg.Sync.KeepRuns)
	}
	if cfg.Bilibili.RequestsPerSecond != 0.5 {
		t.Errorf("requests_per_second = %v, want 0.5", cfg.Bilibili.RequestsPerSecond)
	}
	if want := (DanmakuFile{MaxFiles: 200, MaxFileMB: 64, MaxUploadMB: 256}); cfg.DanmakuFile != want {
		t.Errorf("danmaku_file = %+v, want %+v", cfg.DanmakuFile, want)
	}
}

func TestLoadDandanplayToken(t *testing.T) {
	for _, token := range []string{"", "Abc-1.2_3~", "..."} {
		t.Run(token, func(t *testing.T) {
			setEnv(t, map[string]string{"DATABASE_DSN": "postgres://localhost/danfuse", "DANDANPLAY_TOKEN": token})
			cfg, err := Load("")
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Dandanplay.Token != token {
				t.Errorf("token = %q, want %q", cfg.Dandanplay.Token, token)
			}
		})
	}
}

func TestLoadAcceptsMaxConns(t *testing.T) {
	for _, maxConns := range []string{"0", "1"} { // 0 表示用 pgx 的默认值
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
		{"token 含斜杠", map[string]string{"DANDANPLAY_TOKEN": "a/b"}, "dandanplay.token"},
		{"token 含空格", map[string]string{"DANDANPLAY_TOKEN": "a b"}, "dandanplay.token"},
		{"token 含百分号编码", map[string]string{"DANDANPLAY_TOKEN": "a%20b"}, "dandanplay.token"},
		{"token 含问号", map[string]string{"DANDANPLAY_TOKEN": "a?b"}, "dandanplay.token"},
		{"token 含中文", map[string]string{"DANDANPLAY_TOKEN": "令牌"}, "dandanplay.token"},
		{"token 为 .", map[string]string{"DANDANPLAY_TOKEN": "."}, "dandanplay.token"},
		{"token 为 ..", map[string]string{"DANDANPLAY_TOKEN": ".."}, "dandanplay.token"},
		{"max_conns 为负", map[string]string{"DATABASE_MAX_CONNS": "-1"}, "database.max_conns"},
		{"write_timeout 小于 30s", map[string]string{"SERVER_WRITE_TIMEOUT": "25s"}, "server.write_timeout"},
		{"list_timeout 为 0", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_LIST_TIMEOUT", "0"), "jellyfin.list_timeout"},
		{"keep_runs 为 0", map[string]string{"SYNC_KEEP_RUNS": "0"}, "sync.keep_runs"},
		{"requests_per_second 为 0", map[string]string{"BILIBILI_REQUESTS_PER_SECOND": "0"}, "bilibili.requests_per_second"},
		{"requests_per_second 为负", map[string]string{"BILIBILI_REQUESTS_PER_SECOND": "-1"}, "bilibili.requests_per_second"},
		{"max_files 为 0", map[string]string{"DANMAKU_FILE_MAX_FILES": "0"}, "danmaku_file"},
		{"max_file_mb 为 0", map[string]string{"DANMAKU_FILE_MAX_FILE_MB": "0"}, "danmaku_file"},
		{"max_upload_mb 为 0", map[string]string{"DANMAKU_FILE_MAX_UPLOAD_MB": "0"}, "danmaku_file"},
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

// TestLoadRejectsUnsupportedKind kind 不支持时拒绝启动，错误信息列出允许的取值，不带配置的值。
func TestLoadRejectsUnsupportedKind(t *testing.T) {
	for _, kind := range []string{"plex", "Jellyfin"} {
		t.Run(kind, func(t *testing.T) {
			setEnv(t, map[string]string{"DATABASE_DSN": "postgres://localhost/danfuse", "CATALOG_SOURCE_KIND": kind})
			_, err := Load("")
			if err == nil || !strings.Contains(err.Error(), `catalog_source.kind must be empty or "jellyfin"`) || strings.Contains(err.Error(), kind) {
				t.Errorf("err = %v, want 列出允许的取值且不含配置的值", err)
			}
		})
	}
}

func TestLoadBilibiliSessdata(t *testing.T) {
	const sessdata = "1a2b3c4d%2C1790000000%2Cabcde*a1" // 浏览器里看到的样子：逗号编码成了 %2C
	for _, raw := range []string{"", sessdata, " " + sessdata + "\n"} {
		t.Run(raw, func(t *testing.T) {
			setEnv(t, map[string]string{"DATABASE_DSN": "postgres://localhost/danfuse", "BILIBILI_SESSDATA": raw})
			cfg, err := Load("")
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.TrimSpace(raw); cfg.Bilibili.Sessdata != want {
				t.Errorf("sessdata = %q, want %q", cfg.Bilibili.Sessdata, want)
			}
		})
	}
}

// TestLoadRejectsInvalidSessdata 含有 Cookie 值不允许的字符时拒绝启动，错误信息里不带 SESSDATA 的值。
func TestLoadRejectsInvalidSessdata(t *testing.T) {
	for _, sessdata := range []string{"abc,1790000000", "abc def", "abc;def", `"abc"`, `abc\def`, "令牌"} {
		t.Run(sessdata, func(t *testing.T) {
			setEnv(t, map[string]string{"DATABASE_DSN": "postgres://localhost/danfuse", "BILIBILI_SESSDATA": sessdata})
			_, err := Load("")
			if err == nil || !strings.Contains(err.Error(), "bilibili.sessdata") || strings.Contains(err.Error(), sessdata) {
				t.Errorf("err = %v, want 提到 bilibili.sessdata 且不含它的值", err)
			}
		})
	}
}
