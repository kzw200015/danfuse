package service

import (
	"context"
	"fmt"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/fulltext"
	"github.com/kzw200015/danfuse/backend/internal/provider"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// LocalProvider 本地 Provider：在目录里搜索季，读取一集所有绑定的弹幕。
type LocalProvider struct {
	store   repository.Store
	sources *source.Registry // 绑定的适配器决定弹幕在哪个平台
}

var _ provider.Provider = (*LocalProvider)(nil)

func NewLocalProvider(store repository.Store, sources *source.Registry) *LocalProvider {
	return &LocalProvider{store: store, sources: sources}
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

// Comments 一集的全部弹幕：读出这一集的绑定（失效的照常参与），逐个取出弹幕，交给 danmaku.Merge
// 校正、跨源去重、排序。集不存在或没有绑定时返回空。
// 不包事务：单个绑定的弹幕一条 SELECT 读完，不会读到一半；查完绑定列表后某个绑定被删除时，它的弹幕读成空的。
func (p *LocalProvider) Comments(ctx context.Context, episodeID int64) ([]danmaku.Item, error) {
	bindings, err := p.store.ListBindingsByEpisode(ctx, episodeID)
	if err != nil {
		return nil, fmt.Errorf("list bindings of episode %d: %w", episodeID, err)
	}
	tracks := make([]danmaku.Track, len(bindings))
	for i, b := range bindings {
		adapter, ok := p.sources.Get(b.Adapter)
		if !ok {
			return nil, fmt.Errorf("binding %d: unknown adapter %q", b.ID, b.Adapter)
		}
		items, err := p.bindingDanmaku(ctx, b)
		if err != nil {
			return nil, err
		}
		tracks[i] = danmaku.Track{BindingID: b.ID, Platform: adapter.Platform(), Offset: b.Offset, Scale: b.Scale, Items: items}
	}
	return danmaku.Merge(tracks), nil
}

// bindingDanmaku 取一个绑定的弹幕，时间未校正。各存储模式在这里分开实现：现在只有 snapshot，读拉取时落库的弹幕；
// 以后的 live 在这里按适配器现取。之后的校正、跨源去重、cid 都与存储模式无关。
func (p *LocalProvider) bindingDanmaku(ctx context.Context, b repository.Binding) ([]danmaku.Danmaku, error) {
	switch b.Mode {
	case "snapshot":
		rows, err := p.store.ListDanmakuByBinding(ctx, b.ID)
		if err != nil {
			return nil, fmt.Errorf("list danmaku of binding %d: %w", b.ID, err)
		}
		items := make([]danmaku.Danmaku, len(rows))
		for i, r := range rows {
			items[i] = danmaku.Danmaku{TimeMs: r.TimeMs, Mode: danmaku.Mode(r.Mode), Color: uint32(r.Color), Text: r.Text, SourceID: r.SourceID}
		}
		return items, nil
	default:
		return nil, fmt.Errorf("binding %d: unknown mode %q", b.ID, b.Mode)
	}
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
