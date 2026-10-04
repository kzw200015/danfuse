package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/pkg/response"
)

// SettingsHandler 只读的配置。没有业务逻辑，直接读配置，不设 service。
type SettingsHandler struct {
	source config.CatalogSource
	sync   config.Sync
}

func NewSettingsHandler(source config.CatalogSource, sync config.Sync) *SettingsHandler {
	return &SettingsHandler{source: source, sync: sync}
}

type settingsResponse struct {
	CatalogSource *catalogSourceSettings `json:"catalogSource"` // nil 表示未配置目录源
	// SyncInterval 定时同步的间隔，单位秒，0 表示关闭。
	// 与管理 API 里其他时长（集的时长、绑定的偏移）一样用秒，前端不用解析 Go 的 Duration 写法。
	SyncInterval float64 `json:"syncInterval"`
}

// catalogSourceSettings 目录源的配置，不含 API key。
type catalogSourceSettings struct {
	Kind      string   `json:"kind"`
	URL       string   `json:"url"`
	Libraries []string `json:"libraries"`
}

// Get GET /api/settings
func (h *SettingsHandler) Get(c *echo.Context) error {
	resp := settingsResponse{SyncInterval: h.sync.Interval.Seconds()}
	// 只读取选中那一种的设置块；kind 为空表示未配置目录源（config 已校验，只能为空或 jellyfin）
	if h.source.Kind == config.KindJellyfin {
		jf := h.source.Jellyfin
		resp.CatalogSource = &catalogSourceSettings{Kind: h.source.Kind, URL: jf.URL, Libraries: jf.Libraries}
	}
	return response.OK(c, resp)
}
