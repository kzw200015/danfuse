package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/fulltext"
	"github.com/kzw200015/danfuse/backend/internal/provider"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// LocalProvider 本地 Provider：在目录里搜索季、按 ID 取季，读取一集所有绑定的弹幕。
type LocalProvider struct {
	store   *repository.Store
	sources *source.Registry // 绑定的适配器决定弹幕在哪个平台
}

var _ provider.Provider = (*LocalProvider)(nil)

func NewLocalProvider(store *repository.Store, sources *source.Registry) *LocalProvider {
	return &LocalProvider{store: store, sources: sources}
}

// Search 关键词按 catalog.ParseName 拆开：标题部分按季的搜索列查找，写明的季号、集号（集号以 q.Episode 优先）
// 在 SQL 里按季号、集号过滤，排序全在 SQL 里；多取一条判断 HasMore，再为返回的季查一次集列表。
// 季有没有绑定都照常返回。标题部分切不出词时返回空。
func (p *LocalProvider) Search(ctx context.Context, q provider.SearchQuery) (provider.SearchResult, error) {
	name := catalog.ParseName(q.Keyword)
	query := fulltext.Query(name.Title)
	if query == "" {
		return provider.SearchResult{}, nil
	}
	episode := q.Episode
	if episode == nil {
		episode = name.Episode
	}
	rows, err := p.store.SearchSeasons(ctx, repository.SearchSeasonsParams{
		Query: query, Season: int32Ptr(name.Season), Episode: int32Ptr(episode), MaxRows: int32(q.MaxSeasons + 1),
	})
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
	episodes, err := p.listEpisodes(ctx, ids)
	if err != nil {
		return provider.SearchResult{}, err
	}
	result.Seasons = make([]provider.Season, 0, len(rows))
	for _, r := range rows {
		s := seasonOf(r, episodes[r.ID])
		if episode != nil {
			s.Episodes = slices.DeleteFunc(s.Episodes, func(e provider.Episode) bool { return e.Number != *episode })
			if len(s.Episodes) == 0 { // 查完季之后这一集被删除了，不返回，保证返回的季都带着这一集
				continue
			}
		}
		result.Seasons = append(result.Seasons, s)
	}
	return result, nil
}

// Season 一季连同它的全部集，与 Search 返回的同一季相同。不包事务：查完季后它被删除时，集列表读成空的。
func (p *LocalProvider) Season(ctx context.Context, id int64) (provider.Season, bool, error) {
	row, err := p.store.GetSeason(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return provider.Season{}, false, nil
	}
	if err != nil {
		return provider.Season{}, false, fmt.Errorf("get season %d: %w", id, err)
	}
	episodes, err := p.listEpisodes(ctx, []int64{id})
	if err != nil {
		return provider.Season{}, false, err
	}
	// GetSeason 的列与 SearchSeasons 相同，行可以直接转换；两边的列不一致时编译不通过
	return seasonOf(repository.SearchSeasonsRow(row), episodes[id]), true, nil
}

// listEpisodes 各季的全部集，按季 ID 分组，组内按集号升序。
func (p *LocalProvider) listEpisodes(ctx context.Context, seasonIDs []int64) (map[int64][]provider.Episode, error) {
	rows, err := p.store.ListEpisodesBySeasons(ctx, seasonIDs)
	if err != nil {
		return nil, fmt.Errorf("list episodes of seasons: %w", err)
	}
	bySeason := make(map[int64][]provider.Episode, len(seasonIDs)) // 查询已按集号排序
	for _, e := range rows {
		bySeason[e.SeasonID] = append(bySeason[e.SeasonID], provider.Episode{ID: e.ID, Number: int(e.Number), Title: emptyIfNull(e.Title)})
	}
	return bySeason, nil
}

// seasonOf 由查出的一季（季号与所属剧的类型、剧名、原名、年份）和它的全部集组装一季，名称和类别按目录的规则得出。
func seasonOf(r repository.SearchSeasonsRow, episodes []provider.Episode) provider.Season {
	typ, n := catalog.SeriesType(r.Type), int(r.Number)
	titles := []string{r.Title}
	if r.OriginalTitle != nil {
		titles = append(titles, *r.OriginalTitle)
	}
	return provider.Season{
		ID:           r.ID,
		Name:         catalog.SeasonName(typ, r.Title, n),
		Titles:       titles,
		Kind:         seasonKind(typ, n),
		Year:         intPtr(r.Year),
		Episodes:     episodes,
		EpisodeCount: len(episodes),
	}
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
		adapter, err := p.sources.Get(b.Adapter)
		if err != nil {
			return nil, fmt.Errorf("binding %d: %w", b.ID, err)
		}
		items, err := p.bindingDanmaku(ctx, b.ID)
		if err != nil {
			return nil, err
		}
		tracks[i] = danmaku.Track{BindingID: b.ID, Platform: adapter.Platform(), Offset: b.Offset, Scale: b.Scale, Items: items}
	}
	return danmaku.Merge(tracks), nil
}

// bindingDanmaku 取一个绑定落库的弹幕，时间未校正。播放时不向平台现取（不做 live 存储模式，见 ADR 0002）。
func (p *LocalProvider) bindingDanmaku(ctx context.Context, bindingID int64) ([]danmaku.Danmaku, error) {
	rows, err := p.store.ListDanmakuByBinding(ctx, bindingID)
	if err != nil {
		return nil, fmt.Errorf("list danmaku of binding %d: %w", bindingID, err)
	}
	items := make([]danmaku.Danmaku, len(rows))
	for i, r := range rows {
		items[i] = danmaku.Danmaku{TimeMs: r.TimeMs, Mode: danmaku.Mode(r.Mode), Color: uint32(r.Color), Text: r.Text, SourceID: r.SourceID}
	}
	return items, nil
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
