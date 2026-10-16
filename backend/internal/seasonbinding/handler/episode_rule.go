package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

type defaultEpisodeRuleResponse struct {
	EpisodePatterns []string `json:"episodePatterns"`
}

// DefaultEpisodeRule GET /api/episode-rules/default
// 默认的集号规则：管理界面预览时作为编辑的起点，"恢复默认"时取它。
func (h *Handler) DefaultEpisodeRule(c *echo.Context) error {
	return response.OK(c, defaultEpisodeRuleResponse{EpisodePatterns: source.DefaultEpisodeRule().Patterns()})
}
