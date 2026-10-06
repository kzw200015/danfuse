// Package config 基于 viper 加载应用配置：配置文件 + 环境变量覆盖。
package config

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// envPrefix 环境变量前缀，例如 DANFUSE_DATABASE_DSN 覆盖 database.dsn。
const envPrefix = "DANFUSE"

type Config struct {
	Server        Server        `mapstructure:"server"`
	Log           Log           `mapstructure:"log"`
	Database      Database      `mapstructure:"database"`
	Dandanplay    Dandanplay    `mapstructure:"dandanplay"`
	CatalogSource CatalogSource `mapstructure:"catalog_source"`
	Sync          Sync          `mapstructure:"sync"`
	Bilibili      Bilibili      `mapstructure:"bilibili"`
	DanmakuFile   DanmakuFile   `mapstructure:"danmaku_file"`
}

type Server struct {
	Addr            string        `mapstructure:"addr"`
	GracefulTimeout time.Duration `mapstructure:"graceful_timeout"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"` // 0 表示不限；否则不能小于 MinWriteTimeout
}

// MinWriteTimeout server.write_timeout 的下限：创建绑定、重新拉取要当场拉取弹幕，最长 25 秒（service.fetchTimeout），
// 再留出写库和响应的时间。
const MinWriteTimeout = 30 * time.Second

type Log struct {
	Level  string `mapstructure:"level"`  // debug | info | warn | error
	Format string `mapstructure:"format"` // text | json
}

type Database struct {
	DSN             string        `mapstructure:"dsn"`
	MaxConns        int32         `mapstructure:"max_conns"`
	MinConns        int32         `mapstructure:"min_conns"`
	MaxConnLifetime time.Duration `mapstructure:"max_conn_lifetime"`
	MaxConnIdleTime time.Duration `mapstructure:"max_conn_idle_time"`
}

// Dandanplay 弹弹 API。
type Dandanplay struct {
	// Token 可选；设置后弹弹 API 挂在 /dandanplay/<token>/api/v2，否则为 /dandanplay/api/v2。
	// 只能用 URL 路径里不需要转义的字符，不写日志。
	Token string `mapstructure:"token"`
}

// CatalogSource 目录源。同一时间只有一个，用 Kind 选择种类，只读取选中那一种的设置块。
type CatalogSource struct {
	Kind     string   `mapstructure:"kind"` // 为空表示不启用同步；目前只支持 jellyfin
	Jellyfin Jellyfin `mapstructure:"jellyfin"`
}

// KindJellyfin 目录源种类：Jellyfin 10.11 及以上。
const KindJellyfin = "jellyfin"

type Jellyfin struct {
	URL       string   `mapstructure:"url"`       // 可带子路径，加载时去掉末尾的 /
	APIKey    string   `mapstructure:"api_key"`   // 只作为请求头发送，不写日志
	Libraries []string `mapstructure:"libraries"` // 要同步的媒体库名；环境变量里用逗号分隔
	// ListTimeout 列出媒体库、剧和电影、季和集，每个请求的超时。媒体库很大或 Jellyfin 很慢时调大
	ListTimeout time.Duration `mapstructure:"list_timeout"`
}

type Sync struct {
	Interval time.Duration `mapstructure:"interval"`  // 定时同步的间隔，0 表示关闭
	KeepRuns int32         `mapstructure:"keep_runs"` // 同步记录保留最近几次，至少 1
}

// Bilibili B 站源适配器。
type Bilibili struct {
	// Sessdata 可选的 B 站登录凭据（浏览器 Cookie 里 SESSDATA 的值），只作为 Cookie 发给 B 站，不写日志、不入库。
	// 未配置时以未登录的身份拉取，弹幕可能不全。
	Sessdata string `mapstructure:"sessdata"`
	// RequestsPerSecond 请求 B 站的速率上限（全局令牌桶，所有绑定共用），可以是小数。被 B 站限流时调小
	RequestsPerSecond float64 `mapstructure:"requests_per_second"`
}

// DanmakuFile 上传弹幕文件的上限，按一次上传计；一个绑定累计追加的文件不设上限。
type DanmakuFile struct {
	MaxFiles    int   `mapstructure:"max_files"`     // 一次最多几份
	MaxFileMB   int64 `mapstructure:"max_file_mb"`   // 单份的上限，单位 MB
	MaxUploadMB int64 `mapstructure:"max_upload_mb"` // 一次合计的上限，单位 MB
}

// tokenPattern dandanplay.token 允许的字符：RFC 3986 的 unreserved，放在 URL 路径里不需要转义。
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]*$`)

// Load 读取配置。path 为空时仅使用默认值和环境变量。
func Load(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)

	v.SetEnvPrefix(envPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}
	cfg.normalize()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// setDefaults 为所有配置项设置默认值。
// 注意：viper 的 AutomaticEnv 只对已知 key 生效，新增配置项时务必在这里登记默认值，环境变量才能覆盖。
func setDefaults(v *viper.Viper) {
	v.SetDefault("server.addr", ":8080")
	v.SetDefault("server.graceful_timeout", 10*time.Second)
	v.SetDefault("server.read_timeout", 30*time.Second)
	v.SetDefault("server.write_timeout", 30*time.Second)

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "text")

	v.SetDefault("database.dsn", "")
	v.SetDefault("database.max_conns", 10)
	v.SetDefault("database.min_conns", 1)
	v.SetDefault("database.max_conn_lifetime", time.Hour)
	v.SetDefault("database.max_conn_idle_time", 30*time.Minute)

	v.SetDefault("dandanplay.token", "")

	v.SetDefault("catalog_source.kind", "")
	v.SetDefault("catalog_source.jellyfin.url", "")
	v.SetDefault("catalog_source.jellyfin.api_key", "")
	v.SetDefault("catalog_source.jellyfin.libraries", []string{})
	v.SetDefault("catalog_source.jellyfin.list_timeout", 2*time.Minute)

	v.SetDefault("sync.interval", time.Duration(0))
	v.SetDefault("sync.keep_runs", 20)

	v.SetDefault("bilibili.sessdata", "")
	v.SetDefault("bilibili.requests_per_second", 3.0)

	v.SetDefault("danmaku_file.max_files", 50)
	v.SetDefault("danmaku_file.max_file_mb", 10)
	v.SetDefault("danmaku_file.max_upload_mb", 50)
}

// normalize 规整配置值：url 去掉末尾的 /；媒体库名去掉首尾空白，丢弃空项（例如环境变量末尾多了逗号）；SESSDATA 去掉首尾空白。
func (c *Config) normalize() {
	c.Bilibili.Sessdata = strings.TrimSpace(c.Bilibili.Sessdata)
	jf := &c.CatalogSource.Jellyfin
	jf.URL = strings.TrimRight(strings.TrimSpace(jf.URL), "/")
	libraries := make([]string, 0, len(jf.Libraries))
	for _, name := range jf.Libraries {
		if name = strings.TrimSpace(name); name != "" {
			libraries = append(libraries, name)
		}
	}
	jf.Libraries = libraries
}

func (c *Config) validate() error {
	if t := c.Server.WriteTimeout; t != 0 && t < MinWriteTimeout {
		return fmt.Errorf("config: server.write_timeout must be 0 (no limit) or at least %v: fetching danmaku takes up to 25s", MinWriteTimeout)
	}
	if c.Database.DSN == "" {
		return errors.New("config: database.dsn is required")
	}
	if c.Database.MaxConns < 0 {
		return errors.New("config: database.max_conns must not be negative (0 means the pgx default)")
	}

	// "." 和 ".." 在路径里表示当前目录、上级目录，会被浏览器和反向代理改写
	if t := c.Dandanplay.Token; !tokenPattern.MatchString(t) || t == "." || t == ".." {
		return errors.New("config: dandanplay.token may only contain letters, digits, '-', '.', '_' and '~', and must not be '.' or '..'")
	}

	switch c.CatalogSource.Kind {
	case "":
	case KindJellyfin:
		jf := c.CatalogSource.Jellyfin
		if u, err := url.Parse(jf.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("config: catalog_source.jellyfin.url must be an http(s) URL")
		} else if u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			// 请求的路径直接拼在 url 后面；api_key 只能放在 api_key 里，不能写进查询串
			return errors.New("config: catalog_source.jellyfin.url must not contain a query or fragment")
		}
		if jf.APIKey == "" {
			return errors.New("config: catalog_source.jellyfin.api_key is required")
		}
		if len(jf.Libraries) == 0 {
			return errors.New("config: catalog_source.jellyfin.libraries is required")
		}
		if jf.ListTimeout <= 0 {
			return errors.New("config: catalog_source.jellyfin.list_timeout must be positive")
		}
	default:
		return fmt.Errorf("config: catalog_source.kind must be empty or %q", KindJellyfin)
	}

	if c.Sync.Interval < 0 {
		return errors.New("config: sync.interval must not be negative")
	}
	if c.Sync.Interval > 0 && c.CatalogSource.Kind == "" {
		return errors.New("config: sync.interval requires catalog_source.kind")
	}
	if c.Sync.KeepRuns < 1 {
		return errors.New("config: sync.keep_runs must be at least 1")
	}
	// 原样作为 Cookie 的值发送：只能是浏览器里看到的那一串（逗号编码成了 %2C）。
	// 含有 Cookie 值不允许的字符时 net/http 会加引号或丢掉这些字符，登录态悄悄失效，不如启动时就报错
	if strings.ContainsFunc(c.Bilibili.Sessdata, func(r rune) bool { return !isCookieOctet(r) }) {
		return errors.New("config: bilibili.sessdata must be the cookie value as shown in the browser (no spaces, commas, semicolons, quotes or backslashes)")
	}
	if c.Bilibili.RequestsPerSecond <= 0 {
		return errors.New("config: bilibili.requests_per_second must be positive")
	}
	if df := c.DanmakuFile; df.MaxFiles < 1 || df.MaxFileMB < 1 || df.MaxUploadMB < 1 {
		return errors.New("config: danmaku_file.max_files, max_file_mb and max_upload_mb must be at least 1")
	}
	return nil
}

// isCookieOctet RFC 6265 允许出现在 Cookie 值里的字符：可见的 ASCII 字符，除去 " , ; \。
func isCookieOctet(r rune) bool {
	return r > ' ' && r < 0x7f && r != '"' && r != ',' && r != ';' && r != '\\'
}
