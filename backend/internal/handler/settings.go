package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
)

// SettingsHandler 只读的配置。没有业务逻辑，直接读配置，不设 service。
// 拿整份配置：多返回一项配置时只改 Get，不改构造函数；不该返回的（API key、SESSDATA 的值）由 Get 自己挑出去。
type SettingsHandler struct {
	cfg *config.Config
}

func NewSettingsHandler(cfg *config.Config) *SettingsHandler {
	return &SettingsHandler{cfg: cfg}
}

type settingsResponse struct {
	// DandanplayToken 弹弹 API 的 token，nil 表示没有设置。管理界面用它拼出插件地址；
	// 管理 API 只在内网访问，所以可以返回 token。
	DandanplayToken *string                `json:"dandanplayToken"`
	CatalogSource   *catalogSourceSettings `json:"catalogSource"` // nil 表示未配置目录源
	// SyncInterval 定时同步的间隔，单位秒，0 表示关闭。
	// 与管理 API 里其他时长（集的时长、绑定的偏移）一样用秒，前端不用解析 Go 的 Duration 写法。
	SyncInterval float64 `json:"syncInterval"`
	// BilibiliSessdataConfigured 是否配置了 B 站的 SESSDATA。只给出是否配置，不返回值：它就是账号的登录凭据。
	BilibiliSessdataConfigured bool `json:"bilibiliSessdataConfigured"`
	// Follow 追更的时间规则，管理界面据此写出追更的说明。
	Follow followSettings `json:"follow"`
	// ScheduledFetch 定时拉取的时间规则，管理界面据此写出定时拉取的说明。
	ScheduledFetch scheduledFetchSettings `json:"scheduledFetch"`
}

// followSettings 追更的时间规则，单位秒。
type followSettings struct {
	ScanInterval  float64 `json:"scanInterval"`
	CheckInterval float64 `json:"checkInterval"`
}

// scheduledFetchSettings 定时拉取的时间规则，单位秒。
type scheduledFetchSettings struct {
	Interval float64 `json:"interval"`
	Window   float64 `json:"window"` // 0 表示关闭定时拉取
}

// catalogSourceSettings 目录源的配置，不含 API key。
type catalogSourceSettings struct {
	Kind      string   `json:"kind"`
	URL       string   `json:"url"`
	Libraries []string `json:"libraries"`
}

// Get GET /api/settings
func (h *SettingsHandler) Get(c *echo.Context) error {
	cfg := h.cfg
	resp := settingsResponse{
		SyncInterval:               cfg.Sync.Interval.Seconds(),
		BilibiliSessdataConfigured: cfg.Bilibili.Sessdata != "",
		Follow: followSettings{
			ScanInterval:  cfg.Follow.ScanInterval.Seconds(),
			CheckInterval: cfg.Follow.CheckInterval.Seconds(),
		},
		ScheduledFetch: scheduledFetchSettings{
			Interval: cfg.ScheduledFetch.Interval.Seconds(),
			Window:   cfg.ScheduledFetch.Window.Seconds(),
		},
	}
	if cfg.Dandanplay.Token != "" {
		resp.DandanplayToken = &cfg.Dandanplay.Token
	}
	// 只读取选中那一种的设置块；kind 为空表示未配置目录源（config 已校验，只能为空或 jellyfin）
	if cfg.CatalogSource.Kind == config.KindJellyfin {
		jf := cfg.CatalogSource.Jellyfin
		resp.CatalogSource = &catalogSourceSettings{Kind: cfg.CatalogSource.Kind, URL: jf.URL, Libraries: jf.Libraries}
	}
	return response.OK(c, resp)
}
