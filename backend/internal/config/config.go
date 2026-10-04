// Package config 基于 viper 加载应用配置：配置文件 + 环境变量覆盖。
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// EnvPrefix 环境变量前缀，例如 DANFUSE_DATABASE_DSN 覆盖 database.dsn。
const EnvPrefix = "DANFUSE"

type Config struct {
	Server        Server        `mapstructure:"server"`
	Log           Log           `mapstructure:"log"`
	Database      Database      `mapstructure:"database"`
	CatalogSource CatalogSource `mapstructure:"catalog_source"`
	Sync          Sync          `mapstructure:"sync"`
}

type Server struct {
	Addr            string        `mapstructure:"addr"`
	GracefulTimeout time.Duration `mapstructure:"graceful_timeout"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
}

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
}

type Sync struct {
	Interval time.Duration `mapstructure:"interval"` // 定时同步的间隔，0 表示关闭
}

// Load 读取配置。path 为空时仅使用默认值和环境变量。
func Load(path string) (*Config, error) {
	v := viper.New()
	setDefaults(v)

	v.SetEnvPrefix(EnvPrefix)
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

	v.SetDefault("catalog_source.kind", "")
	v.SetDefault("catalog_source.jellyfin.url", "")
	v.SetDefault("catalog_source.jellyfin.api_key", "")
	v.SetDefault("catalog_source.jellyfin.libraries", []string{})

	v.SetDefault("sync.interval", time.Duration(0))
}

// normalize 规整配置值：url 去掉末尾的 /；媒体库名去掉首尾空白，丢弃空项（例如环境变量末尾多了逗号）。
func (c *Config) normalize() {
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
	if c.Database.DSN == "" {
		return errors.New("config: database.dsn is required")
	}
	// 同步用一个专用连接持有同步锁直到结束，写目录还要再取连接；只有 1 个连接时同步会一直等自己
	if c.Database.MaxConns != 0 && c.Database.MaxConns < 2 {
		return errors.New("config: database.max_conns must be 0 (default) or at least 2: a running sync holds one connection for its lock")
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
	default:
		return fmt.Errorf("config: unsupported catalog_source.kind %q (want empty or %q)", c.CatalogSource.Kind, KindJellyfin)
	}

	if c.Sync.Interval < 0 {
		return errors.New("config: sync.interval must not be negative")
	}
	if c.Sync.Interval > 0 && c.CatalogSource.Kind == "" {
		return errors.New("config: sync.interval requires catalog_source.kind")
	}
	return nil
}
