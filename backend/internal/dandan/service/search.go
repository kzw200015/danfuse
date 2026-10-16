package service

import (
	"context"
	"fmt"

	"github.com/kzw200015/danfuse/backend/internal/dandan/dao"
	"github.com/kzw200015/danfuse/backend/internal/fulltext"
	"github.com/kzw200015/danfuse/backend/internal/naming"
)

// SearchQuery 搜索条件。
type SearchQuery struct {
	// Keyword 用户输入的原文（或 match 的文件名），不清洗、不分词；
	// 其中写明了的季号、集号按 naming.Parse 认出，只返回这一季、这一集。
	Keyword    string
	MaxSeasons int
	Episode    *int // 集号，优先于 Keyword 里写明的集号（协议参数比关键词里推断的更明确）
}

// SearchResult 搜索结果。按集号过滤时（SearchQuery.Episode 或关键词里写明的集号），只返回有这一集的季，
// 每季的 Episodes 恰好是这一集。
type SearchResult struct {
	Seasons []Season
	HasMore bool
}

// Search 关键词按 naming.Parse 拆开：标题部分按季的搜索列查找，写明的季号、集号（集号以 q.Episode 优先）
// 在 SQL 里按季号、集号过滤，排序全在 SQL 里；多取一条判断 HasMore，再为返回的季查一次集列表。
// 季有没有绑定都照常返回。标题部分切不出词时返回空。
func (d *Service) Search(ctx context.Context, q SearchQuery) (SearchResult, error) {
	name := naming.Parse(q.Keyword)
	episode := q.Episode
	if episode == nil {
		episode = name.Episode
	}
	return d.search(ctx, name, episode, q.MaxSeasons)
}

// search 按拆开的名称搜索，episode 为要过滤的集号（nil 为不按集号过滤），供 Search 与 Match 共用。
func (d *Service) search(ctx context.Context, name naming.Parsed, episode *int, maxSeasons int) (SearchResult, error) {
	query := fulltext.Query(name.Title)
	if query == "" {
		return SearchResult{}, nil
	}
	rows, err := d.q.SearchSeasons(ctx, dao.SearchSeasonsParams{
		Query: query, Season: int32Ptr(name.Season), Episode: int32Ptr(episode), MaxRows: int32(maxSeasons + 1),
	})
	if err != nil {
		return SearchResult{}, fmt.Errorf("search seasons: %w", err)
	}
	if len(rows) == 0 {
		return SearchResult{}, nil
	}
	result := SearchResult{HasMore: len(rows) > maxSeasons}
	rows = rows[:min(len(rows), maxSeasons)]

	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	episodes, err := d.listEpisodes(ctx, ids, episode)
	if err != nil {
		return SearchResult{}, err
	}
	result.Seasons = make([]Season, 0, len(rows))
	for _, r := range rows {
		e := episodes[r.ID]
		if episode != nil && len(e.list) == 0 { // 查完季之后这一集被删除了，不返回，保证返回的季都带着这一集
			continue
		}
		result.Seasons = append(result.Seasons, seasonOf(r, e))
	}
	return result, nil
}
