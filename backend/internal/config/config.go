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
	Server         Server         `mapstructure:"server"`
	Log            Log            `mapstructure:"log"`
	Database       Database       `mapstructure:"database"`
	Dandanplay     Dandanplay     `mapstructure:"dandanplay"`
	CatalogSource  CatalogSource  `mapstructure:"catalog_source"`
	Sync           Sync           `mapstructure:"sync"`
	Follow         Follow         `mapstructure:"follow"`
	ScheduledFetch ScheduledFetch `mapstructure:"scheduled_fetch"`
	Bilibili       Bilibili       `mapstructure:"bilibili"`
	DanmakuFile    DanmakuFile    `mapstructure:"danmaku_file"`
}

type Server struct {
	Addr            string        `mapstructure:"addr"`
	GracefulTimeout time.Duration `mapstructure:"graceful_timeout"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`  // 读整个请求（含上传的文件）的超时，0（默认）表示不限
	WriteTimeout    time.Duration `mapstructure:"write_timeout"` // 从读完请求头到写完响应的超时，0（默认）表示不限；否则不能小于 MinWriteTimeout
}

// MinWriteTimeout server.write_timeout 的下限：创建绑定、重新拉取要当场拉取弹幕，最长 25 秒（source.FetchTimeout），
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
	ConnectTimeout  time.Duration `mapstructure:"connect_timeout"` // 启动时建立连接池、检查连通性的超时
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
	// PosterTimeout 下载一张海报的超时
	PosterTimeout time.Duration `mapstructure:"poster_timeout"`
}

type Sync struct {
	Interval time.Duration `mapstructure:"interval"`  // 定时同步的间隔，0 表示关闭
	KeepRuns int32         `mapstructure:"keep_runs"` // 同步记录保留最近几次，至少 1
}

// Follow 季绑定追更的时间规则。
type Follow struct {
	// ScanInterval 后台扫描的间隔：目录同步进来新的集后，最迟过这么久补建
	ScanInterval time.Duration `mapstructure:"scan_interval"`
	// CheckInterval 检查合集的周期
	CheckInterval time.Duration `mapstructure:"check_interval"`
}

// ScheduledFetch 定时拉取的时间规则：能重新拉取的绑定，建出后 Window 之内，距上次尝试拉取满 Interval 时自动重新拉取。
type ScheduledFetch struct {
	// Interval 每个绑定两次定时拉取的最短间隔，从上次尝试拉取（不论成败，包括手动重新拉取）算起
	Interval time.Duration `mapstructure:"interval"`
	// Window 绑定建出后这么久之内定时拉取；0 表示关闭定时拉取
	Window time.Duration `mapstructure:"window"`
}

// Bilibili B 站源适配器。
type Bilibili struct {
	// Sessdata 可选的 B 站登录凭据（浏览器 Cookie 里 SESSDATA 的值），只作为 Cookie 发给 B 站，不写日志、不入库。
	// 未配置时以未登录的身份拉取，弹幕可能不全。
	Sessdata string `mapstructure:"sessdata"`
	// RequestsPerSecond 请求 B 站的平均速率上限（全局令牌桶，所有绑定共用，空闲之后允许连发几个请求），可以是小数。被 B 站限流时调小
	RequestsPerSecond float64 `mapstructure:"requests_per_second"`
	// Burst 全局令牌桶的容量：空闲之后最多连发几个请求，贴链接、重新拉取时开头几个请求不排队
	Burst int `mapstructure:"burst"`
	// FetchConcurrency 拉取一个弹幕源时同时进行的请求数（分段与 XML 一起算）
	FetchConcurrency int `mapstructure:"fetch_concurrency"`
	// RequestTimeout 单个请求的超时，超时后照常重试；整次拉取的总时限另见 source.FetchTimeout
	RequestTimeout time.Duration `mapstructure:"request_timeout"`
}

// maxMultipartParts Go 解析 multipart 时一个请求最多的 part 数（mime/multipart 的默认值，GODEBUG multipartmaxparts）。
const maxMultipartParts = 1000

// DanmakuFile 上传弹幕文件的上限，按一次上传计；一个绑定累计追加的文件不设上限。
// 按季上传另有份数与合计的上限，单份仍受 MaxFileMB 限制。
type DanmakuFile struct {
	MaxFiles          int   `mapstructure:"max_files"`            // 一次最多几份，不超过 1000
	MaxFileMB         int64 `mapstructure:"max_file_mb"`          // 单份的上限，单位 MB
	MaxUploadMB       int64 `mapstructure:"max_upload_mb"`        // 一次合计的上限，单位 MB
	SeasonMaxFiles    int   `mapstructure:"season_max_files"`     // 按季上传一次最多几份，不超过 998
	SeasonMaxUploadMB int64 `mapstructure:"season_max_upload_mb"` // 按季上传一次合计的上限，单位 MB
}

// tokenPattern dandanplay.token 允许的字符：RFC 3986 的 unreserved，放在 URL 路径里不需要转义。
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]*$`)

// Defaults 全部取默认值的配置：不读配置文件和环境变量，也不校验（database.dsn 为空）。
// 测试用它构造适配器和 service，只改关心的字段，不手抄默认值。
func Defaults() Config {
	v := viper.New()
	setDefaults(v)
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		panic(fmt.Sprintf("config: unmarshal defaults: %v", err))
	}
	return cfg
}

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
	v.SetDefault("server.read_timeout", time.Duration(0))
	v.SetDefault("server.write_timeout", time.Duration(0))

	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "text")

	v.SetDefault("database.dsn", "")
	v.SetDefault("database.max_conns", 10)
	v.SetDefault("database.min_conns", 1)
	v.SetDefault("database.max_conn_lifetime", time.Hour)
	v.SetDefault("database.max_conn_idle_time", 30*time.Minute)
	v.SetDefault("database.connect_timeout", 10*time.Second)

	v.SetDefault("dandanplay.token", "")

	v.SetDefault("catalog_source.kind", "")
	v.SetDefault("catalog_source.jellyfin.url", "")
	v.SetDefault("catalog_source.jellyfin.api_key", "")
	v.SetDefault("catalog_source.jellyfin.libraries", []string{})
	v.SetDefault("catalog_source.jellyfin.list_timeout", 2*time.Minute)
	v.SetDefault("catalog_source.jellyfin.poster_timeout", 30*time.Second)

	v.SetDefault("sync.interval", time.Duration(0))
	v.SetDefault("sync.keep_runs", 20)

	v.SetDefault("follow.scan_interval", time.Minute)
	v.SetDefault("follow.check_interval", 12*time.Hour)

	v.SetDefault("scheduled_fetch.interval", 12*time.Hour)
	v.SetDefault("scheduled_fetch.window", 14*24*time.Hour)

	v.SetDefault("bilibili.sessdata", "")
	v.SetDefault("bilibili.requests_per_second", 3.0)
	v.SetDefault("bilibili.burst", 10)
	v.SetDefault("bilibili.fetch_concurrency", 10)
	v.SetDefault("bilibili.request_timeout", 10*time.Second)

	v.SetDefault("danmaku_file.max_files", 50)
	v.SetDefault("danmaku_file.max_file_mb", 10)
	v.SetDefault("danmaku_file.max_upload_mb", 50)
	v.SetDefault("danmaku_file.season_max_files", 500)
	v.SetDefault("danmaku_file.season_max_upload_mb", 200)
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
	if c.Database.ConnectTimeout <= 0 {
		return errors.New("config: database.connect_timeout must be positive")
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
		if jf.PosterTimeout <= 0 {
			return errors.New("config: catalog_source.jellyfin.poster_timeout must be positive")
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
	if c.Follow.ScanInterval <= 0 {
		return errors.New("config: follow.scan_interval must be positive")
	}
	if c.Follow.CheckInterval <= 0 {
		return errors.New("config: follow.check_interval must be positive")
	}
	if c.ScheduledFetch.Interval <= 0 {
		return errors.New("config: scheduled_fetch.interval must be positive")
	}
	if c.ScheduledFetch.Window < 0 {
		return errors.New("config: scheduled_fetch.window must not be negative (0 disables scheduled fetching)")
	}
	// 原样作为 Cookie 的值发送：只能是浏览器里看到的那一串（逗号编码成了 %2C）。
	// 含有 Cookie 值不允许的字符时 net/http 会加引号或丢掉这些字符，登录态悄悄失效，不如启动时就报错
	if strings.ContainsFunc(c.Bilibili.Sessdata, func(r rune) bool { return !isCookieOctet(r) }) {
		return errors.New("config: bilibili.sessdata must be the cookie value as shown in the browser (no spaces, commas, semicolons, quotes or backslashes)")
	}
	if c.Bilibili.RequestsPerSecond <= 0 {
		return errors.New("config: bilibili.requests_per_second must be positive")
	}
	if c.Bilibili.Burst < 1 {
		return errors.New("config: bilibili.burst must be at least 1")
	}
	if c.Bilibili.FetchConcurrency < 1 {
		return errors.New("config: bilibili.fetch_concurrency must be at least 1")
	}
	if c.Bilibili.RequestTimeout <= 0 {
		return errors.New("config: bilibili.request_timeout must be positive")
	}
	if df := c.DanmakuFile; df.MaxFiles < 1 || df.MaxFileMB < 1 || df.MaxUploadMB < 1 || df.SeasonMaxFiles < 1 || df.SeasonMaxUploadMB < 1 {
		return errors.New("config: danmaku_file.max_files, max_file_mb, max_upload_mb, season_max_files and season_max_upload_mb must be at least 1")
	}
	// Go 解析 multipart 时一个请求最多 1000 个 part，超出时只能报"请求参数错误"：
	// 单集上传每份文件一个 part，按季上传另有 paths、targets 两个字段
	if c.DanmakuFile.MaxFiles > maxMultipartParts {
		return fmt.Errorf("config: danmaku_file.max_files must be at most %d (Go's multipart limit of %d parts per request)", maxMultipartParts, maxMultipartParts)
	}
	if c.DanmakuFile.SeasonMaxFiles > maxMultipartParts-2 {
		return fmt.Errorf("config: danmaku_file.season_max_files must be at most %d (Go's multipart limit of %d parts per request, minus the paths and targets fields)", maxMultipartParts-2, maxMultipartParts)
	}
	return nil
}

// isCookieOctet RFC 6265 允许出现在 Cookie 值里的字符：可见的 ASCII 字符，除去 " , ; \。
func isCookieOctet(r rune) bool {
	return r > ' ' && r < 0x7f && r != '"' && r != ',' && r != ';' && r != '\\'
}
