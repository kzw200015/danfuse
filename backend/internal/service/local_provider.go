package service

import (
	"context"
	"fmt"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/fulltext"
	"github.com/kzw200015/danfuse/backend/internal/provider"
	"github.com/kzw200015/danfuse/backend/internal/repository"
)

// LocalProvider 本地 Provider：在目录里搜索季，读取一集所有绑定的弹幕。
type LocalProvider struct {
	store repository.Store
}

var _ provider.Provider = (*LocalProvider)(nil)

func NewLocalProvider(store repository.Store) *LocalProvider {
	return &LocalProvider{store: store}
}

// Search 按季的搜索列查找，排序全在 SQL 里；多取一条判断 HasMore，再为返回的季查一次集列表。
// 季有没有绑定都照常返回。关键词切不出词时返回空。
func (p *LocalProvider) Search(ctx context.Context, q provider.SearchQuery) (provider.SearchResult, error) {
	query := fulltext.Query(q.Keyword)
	if query == "" {
		return provider.SearchResult{}, nil
	}
	rows, err := p.store.SearchSeasons(ctx, repository.SearchSeasonsParams{Query: query, MaxRows: int32(q.MaxSeasons + 1)})
	if err != nil {
		return provider.SearchResult{}, fmt.Errorf("search seasons: %w", err)
	}
	if len(rows) == 0 {
		return provider.SearchResult{}, nil
	}
	result := provider.SearchResult{HasMore: len(rows) > q.MaxSeasons}
	rows = rows[:min(len(rows), q.MaxSeasons)]

	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	episodes, err := p.store.ListEpisodesOfSeasons(ctx, ids)
	if err != nil {
		return provider.SearchResult{}, fmt.Errorf("list episodes of seasons: %w", err)
	}
	byID := make(map[int64][]provider.Episode, len(rows)) // 季 ID → 集，查询已按集号排序
	for _, e := range episodes {
		byID[e.SeasonID] = append(byID[e.SeasonID], provider.Episode{ID: e.ID, Number: int(e.Number), Title: emptyIfNull(e.Title)})
	}

	result.Seasons = make([]provider.Season, len(rows))
	for i, r := range rows {
		typ, number := catalog.SeriesType(r.Type), int(r.Number)
		result.Seasons[i] = provider.Season{
			ID:       r.ID,
			Name:     catalog.SeasonName(typ, r.Title, number),
			Kind:     seasonKind(typ, number),
			Year:     intPtr(r.Year),
			Number:   &number,
			Episodes: byID[r.ID],
		}
	}
	return result, nil
}

// Comments 一集的全部弹幕。工单 30 实现绑定弹幕的读取路径之前，恒返回空。
func (p *LocalProvider) Comments(context.Context, int64) ([]danmaku.Item, error) {
	return nil, nil
}

func seasonKind(t catalog.SeriesType, number int) provider.SeasonKind {
	switch {
	case t == catalog.TypeMovie:
		return provider.KindMovie
	case number == 0:
		return provider.KindSpecial
	default:
		return provider.KindSeries
	}
}

func intPtr(v *int32) *int {
	if v == nil {
		return nil
	}
	return new(int(*v))
}
