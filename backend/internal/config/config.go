// Package config 基于 viper 加载应用配置：配置文件 + 环境变量覆盖。
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// EnvPrefix 环境变量前缀，例如 DANFUSE_DATABASE_DSN 覆盖 database.dsn。
const EnvPrefix = "DANFUSE"

type Config struct {
	Server   Server   `mapstructure:"server"`
	Log      Log      `mapstructure:"log"`
	Database Database `mapstructure:"database"`
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
}

func (c *Config) validate() error {
	if c.Database.DSN == "" {
		return errors.New("config: database.dsn is required")
	}
	return nil
}
