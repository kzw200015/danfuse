package provider

import (
	"context"
	"slices"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/fulltext"
)

// Aggregator 聚合层，实现同一个接口，另外按名称识别一集（Match）。现在只有本地 Provider：搜索只问本地；
// 取季、取弹幕按 ID 号段路由，本地以外的号段还没有 Provider，返回空。多个 Provider 的合并规则在接入上游 Provider 时再落实。
type Aggregator struct {
	local Provider
}

var _ Provider = (*Aggregator)(nil)

func NewAggregator(local Provider) *Aggregator {
	return &Aggregator{local: local}
}

func (a *Aggregator) Search(ctx context.Context, q SearchQuery) (SearchResult, error) {
	return a.local.Search(ctx, q)
}

// MatchResult 识别的结果。Candidates 按可能性排序，每个是一季，Episodes 恰好是认出的那一集；
// Exact 为 true 时恰好一个候选，而且名称里的标题就是这部剧的标题或原名，可以直接采用。
type MatchResult struct {
	Candidates []Season
	Exact      bool
}

// Match 按名称（match 的文件名）识别一集。名称里要写明集号（catalog.ParseName），否则没有候选；
// 其余与搜索相同：写明的季号只要这一季，有这一集的季都是候选，按搜索的排序，最多 maxCandidates 个。
// 候选唯一不算确定：名称里的标题可能只是剧名中的一段，标题与剧名或原名的词完全相同（fulltext.SameWords）才是 Exact。
func (a *Aggregator) Match(ctx context.Context, name string, maxCandidates int) (MatchResult, error) {
	parsed := catalog.ParseName(name)
	if parsed.Episode == nil {
		return MatchResult{}, nil
	}
	result, err := a.Search(ctx, SearchQuery{Keyword: name, MaxSeasons: maxCandidates})
	if err != nil {
		return MatchResult{}, err
	}
	exact := len(result.Seasons) == 1 && slices.ContainsFunc(result.Seasons[0].Titles, func(t string) bool {
		return fulltext.SameWords(t, parsed.Title)
	})
	return MatchResult{Candidates: result.Seasons, Exact: exact}, nil
}

func (a *Aggregator) Season(ctx context.Context, id int64) (Season, bool, error) {
	if !isLocal(id) {
		return Season{}, false, nil
	}
	return a.local.Season(ctx, id)
}

func (a *Aggregator) Comments(ctx context.Context, episodeID int64) ([]danmaku.Item, error) {
	if !isLocal(episodeID) {
		return nil, nil
	}
	return a.local.Comments(ctx, episodeID)
}

// isLocal ID 是否在本地号段内。
func isLocal(id int64) bool {
	return id > 0 && id < LocalIDLimit
}
