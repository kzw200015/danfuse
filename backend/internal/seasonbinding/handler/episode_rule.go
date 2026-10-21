package handler

import (
	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/request"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/response"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

type defaultEpisodeRuleResponse struct {
	EpisodePatterns []string                 `json:"episodePatterns"`
	EpisodeRule     source.EpisodeRuleConfig `json:"episodeRule"`
	Templates       []source.EpisodeTemplate `json:"templates"`
}

// DefaultEpisodeRule GET /api/episode-rules/default
// 默认的集号规则：管理界面预览时作为编辑的起点，"恢复默认"时取它。
func (h *Handler) DefaultEpisodeRule(c *echo.Context) error {
	return response.OK(c, defaultEpisodeRuleResponse{
		EpisodePatterns: source.DefaultEpisodeRule().Patterns(),
		EpisodeRule:     source.DefaultEpisodeRuleV2().Config(),
		Templates:       source.EpisodeTemplates(),
	})
}

type episodeTemplateRequest struct {
	ID         string `json:"templateId"`
	Season     *int   `json:"season"`
	Adjustment int64  `json:"adjustment"`
	pattern    source.EpisodePattern
}

func (r *episodeTemplateRequest) Validate() error {
	p, err := source.RenderEpisodeTemplate(r.ID, r.Season, r.Adjustment)
	if err != nil {
		return request.InvalidParam(err.Error())
	}
	r.pattern = p
	return nil
}

// RenderEpisodeTemplate generates an editable snapshot without platform or database IO.
func (h *Handler) RenderEpisodeTemplate(c *echo.Context) error {
	req, err := request.Bind[episodeTemplateRequest](c)
	if err != nil {
		return err
	}
	return response.OK(c, struct {
		Rule    source.EpisodePattern `json:"rule"`
		Example string                `json:"example"`
	}{Rule: req.pattern, Example: source.TemplateExample(req.ID, req.Season)})
}
