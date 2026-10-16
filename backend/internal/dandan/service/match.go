package service

import (
	"context"
	"slices"

	"github.com/kzw200015/danfuse/backend/internal/fulltext"
	"github.com/kzw200015/danfuse/backend/internal/naming"
)

// MatchResult 识别的结果。Candidates 按可能性排序，每个是一季，Episodes 恰好是认出的那一集；
// Exact 为 true 时恰好一个候选，而且名称里的标题就是这部剧的标题或原名，可以直接采用。
// Parsed 是名称按 naming.Parse 认出的标题、季号、集号，Parsed.Episode 为 nil 时没有搜索。
type MatchResult struct {
	Candidates []Season
	Exact      bool
	Parsed     naming.Parsed
}

// Match 按名称（match 的文件名）识别一集。名称里要写明集号（naming.Parse），否则没有候选；
// 其余与搜索相同：写明的季号只要这一季，有这一集的季都是候选，按搜索的排序，最多 maxCandidates 个。
// 候选唯一不算确定：名称里的标题可能只是剧名中的一段，标题与剧名或原名的词完全相同（fulltext.SameWords）才是 Exact。
func (d *Service) Match(ctx context.Context, name string, maxCandidates int) (MatchResult, error) {
	parsed := naming.Parse(name)
	if parsed.Episode == nil {
		return MatchResult{Parsed: parsed}, nil
	}
	result, err := d.search(ctx, parsed, parsed.Episode, maxCandidates)
	if err != nil {
		return MatchResult{}, err
	}
	exact := len(result.Seasons) == 1 && slices.ContainsFunc(result.Seasons[0].Titles, func(t string) bool {
		return fulltext.SameWords(t, parsed.Title)
	})
	return MatchResult{Candidates: result.Seasons, Exact: exact, Parsed: parsed}, nil
}
