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

func TestLoadTunablesFromEnv(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_DSN":                           "postgres://localhost/danfuse",
		"SERVER_WRITE_TIMEOUT":                   "0", // 不限
		"DATABASE_CONNECT_TIMEOUT":               "1m",
		"CATALOG_SOURCE_JELLYFIN_LIST_TIMEOUT":   "10m",
		"CATALOG_SOURCE_JELLYFIN_POSTER_TIMEOUT": "2m",
		"SYNC_KEEP_RUNS":                         "1",
		"FOLLOW_SCAN_INTERVAL":                   "30s",
		"FOLLOW_CHECK_INTERVAL":                  "6h",
		"SCHEDULED_FETCH_INTERVAL":               "1h",
		"SCHEDULED_FETCH_WINDOW":                 "0", // 关闭定时拉取
		"BILIBILI_REQUESTS_PER_SECOND":           "0.5",
		"BILIBILI_BURST":                         "1",
		"BILIBILI_FETCH_CONCURRENCY":             "2",
		"BILIBILI_REQUEST_TIMEOUT":               "20s",
		"DANMAKU_FILE_MAX_FILES":                 "200",
		"DANMAKU_FILE_MAX_FILE_MB":               "64",
		"DANMAKU_FILE_MAX_UPLOAD_MB":             "256",
		"DANMAKU_FILE_SEASON_MAX_FILES":          "1000",
		"DANMAKU_FILE_SEASON_MAX_UPLOAD_MB":      "512",
	})

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.WriteTimeout != 0 {
		t.Errorf("write_timeout = %v, want 0", cfg.Server.WriteTimeout)
	}
	if got := cfg.Database.ConnectTimeout; got != time.Minute {
		t.Errorf("connect_timeout = %v, want 1m", got)
	}
	if got := cfg.CatalogSource.Jellyfin.ListTimeout; got != 10*time.Minute {
		t.Errorf("list_timeout = %v, want 10m", got)
	}
	if got := cfg.CatalogSource.Jellyfin.PosterTimeout; got != 2*time.Minute {
		t.Errorf("poster_timeout = %v, want 2m", got)
	}
	if cfg.Sync.KeepRuns != 1 {
		t.Errorf("keep_runs = %d, want 1", cfg.Sync.KeepRuns)
	}
	if want := (Follow{ScanInterval: 30 * time.Second, CheckInterval: 6 * time.Hour}); cfg.Follow != want {
		t.Errorf("follow = %+v, want %+v", cfg.Follow, want)
	}
	if want := (ScheduledFetch{Interval: time.Hour}); cfg.ScheduledFetch != want {
		t.Errorf("scheduled_fetch = %+v, want %+v", cfg.ScheduledFetch, want)
	}
	if want := (Bilibili{RequestsPerSecond: 0.5, Burst: 1, FetchConcurrency: 2, RequestTimeout: 20 * time.Second}); cfg.Bilibili != want {
		t.Errorf("bilibili = %+v, want %+v", cfg.Bilibili, want)
	}
	if want := (DanmakuFile{MaxFiles: 200, MaxFileMB: 64, MaxUploadMB: 256, SeasonMaxFiles: 1000, SeasonMaxUploadMB: 512}); cfg.DanmakuFile != want {
		t.Errorf("danmaku_file = %+v, want %+v", cfg.DanmakuFile, want)
	}
}

// TestLoadAccepts 边界上合法的取值照常加载；SESSDATA 去掉首尾空白。
func TestLoadAccepts(t *testing.T) {
	const sessdata = "1a2b3c4d%2C1790000000%2Cabcde*a1" // 浏览器里看到的样子：逗号编码成了 %2C
	tests := []struct {
		key, value string
		got        func(*Config) any
		want       any
	}{
		{"DANDANPLAY_TOKEN", "", func(c *Config) any { return c.Dandanplay.Token }, ""},
		{"DANDANPLAY_TOKEN", "Abc-1.2_3~", func(c *Config) any { return c.Dandanplay.Token }, "Abc-1.2_3~"},
		{"DANDANPLAY_TOKEN", "...", func(c *Config) any { return c.Dandanplay.Token }, "..."},
		{"DATABASE_MAX_CONNS", "0", func(c *Config) any { return c.Database.MaxConns }, int32(0)}, // 0 表示用 pgx 的默认值
		{"DATABASE_MAX_CONNS", "1", func(c *Config) any { return c.Database.MaxConns }, int32(1)},
		{"BILIBILI_SESSDATA", "", func(c *Config) any { return c.Bilibili.Sessdata }, ""},
		{"BILIBILI_SESSDATA", sessdata, func(c *Config) any { return c.Bilibili.Sessdata }, sessdata},
		{"BILIBILI_SESSDATA", " " + sessdata + "\n", func(c *Config) any { return c.Bilibili.Sessdata }, sessdata},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.value, func(t *testing.T) {
			setEnv(t, map[string]string{"DATABASE_DSN": "postgres://localhost/danfuse", tt.key: tt.value})
			cfg, err := Load("")
			if err != nil {
				t.Fatal(err)
			}
			if got := tt.got(cfg); got != tt.want {
				t.Errorf("%s = %#v, want %#v", tt.key, got, tt.want)
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
		{"connect_timeout 为 0", map[string]string{"DATABASE_CONNECT_TIMEOUT": "0"}, "database.connect_timeout"},
		{"list_timeout 为 0", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_LIST_TIMEOUT", "0"), "jellyfin.list_timeout"},
		{"poster_timeout 为 0", with(jellyfin, "CATALOG_SOURCE_JELLYFIN_POSTER_TIMEOUT", "0"), "jellyfin.poster_timeout"},
		{"keep_runs 为 0", map[string]string{"SYNC_KEEP_RUNS": "0"}, "sync.keep_runs"},
		{"scan_interval 为 0", map[string]string{"FOLLOW_SCAN_INTERVAL": "0"}, "follow.scan_interval"},
		{"check_interval 为 0", map[string]string{"FOLLOW_CHECK_INTERVAL": "0"}, "follow.check_interval"},
		{"scheduled_fetch.interval 为 0", map[string]string{"SCHEDULED_FETCH_INTERVAL": "0"}, "scheduled_fetch.interval"},
		{"scheduled_fetch.window 为负", map[string]string{"SCHEDULED_FETCH_WINDOW": "-1h"}, "scheduled_fetch.window"},
		{"requests_per_second 为 0", map[string]string{"BILIBILI_REQUESTS_PER_SECOND": "0"}, "bilibili.requests_per_second"},
		{"requests_per_second 为负", map[string]string{"BILIBILI_REQUESTS_PER_SECOND": "-1"}, "bilibili.requests_per_second"},
		{"burst 为 0", map[string]string{"BILIBILI_BURST": "0"}, "bilibili.burst"},
		{"fetch_concurrency 为 0", map[string]string{"BILIBILI_FETCH_CONCURRENCY": "0"}, "bilibili.fetch_concurrency"},
		{"request_timeout 为 0", map[string]string{"BILIBILI_REQUEST_TIMEOUT": "0"}, "bilibili.request_timeout"},
		{"max_files 为 0", map[string]string{"DANMAKU_FILE_MAX_FILES": "0"}, "danmaku_file"},
		{"max_file_mb 为 0", map[string]string{"DANMAKU_FILE_MAX_FILE_MB": "0"}, "danmaku_file"},
		{"max_upload_mb 为 0", map[string]string{"DANMAKU_FILE_MAX_UPLOAD_MB": "0"}, "danmaku_file"},
		{"season_max_files 为 0", map[string]string{"DANMAKU_FILE_SEASON_MAX_FILES": "0"}, "danmaku_file"},
		{"season_max_upload_mb 为 0", map[string]string{"DANMAKU_FILE_SEASON_MAX_UPLOAD_MB": "0"}, "danmaku_file"},
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
