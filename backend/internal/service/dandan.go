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
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/repository/sqlc"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// DandanService 弹弹 API 背后的查询：在目录里搜索季、按名称识别一集、按 ID 取季，读取一集所有绑定的弹幕。
// 返回的是系统内部的结构，不是弹弹play 的 JSON：协议参数的转换与格式化归 dandan 包。
type DandanService struct {
	store   *repository.Store
	sources *source.Registry // 绑定的适配器决定弹幕在哪个平台
}

func NewDandanService(store *repository.Store, sources *source.Registry) *DandanService {
	return &DandanService{store: store, sources: sources}
}

// DandanSearchQuery 搜索条件。
type DandanSearchQuery struct {
	// Keyword 用户输入的原文（或 match 的文件名），不清洗、不分词；
	// 其中写明了的季号、集号按 catalog.ParseName 认出，只返回这一季、这一集。
	Keyword    string
	MaxSeasons int
	Episode    *int // 集号，优先于 Keyword 里写明的集号（协议参数比关键词里推断的更明确）
}

// DandanSearchResult 搜索结果。按集号过滤时（SearchQuery.Episode 或关键词里写明的集号），只返回有这一集的季，
// 每季的 Episodes 恰好是这一集。
type DandanSearchResult struct {
	Seasons []DandanSeason
	HasMore bool
}

type DandanSeason struct {
	ID           int64
	Name         string   // 按 catalog.SeasonName 拼
	Titles       []string // 所属剧的标题，有原名时加上原名；识别时用来判断文件名里的标题是不是这部剧
	Kind         catalog.SeasonKind
	Year         *int
	Episodes     []DandanEpisode // 按集号升序
	EpisodeCount int             // 这一季的总集数；按集号过滤时 Episodes 只有那一集，总集数不变
}

type DandanEpisode struct {
	ID     int64
	Number int
	Title  string // 可以为空
}

// Search 关键词按 catalog.ParseName 拆开：标题部分按季的搜索列查找，写明的季号、集号（集号以 q.Episode 优先）
// 在 SQL 里按季号、集号过滤，排序全在 SQL 里；多取一条判断 HasMore，再为返回的季查一次集列表。
// 季有没有绑定都照常返回。标题部分切不出词时返回空。
func (d *DandanService) Search(ctx context.Context, q DandanSearchQuery) (DandanSearchResult, error) {
	name := catalog.ParseName(q.Keyword)
	episode := q.Episode
	if episode == nil {
		episode = name.Episode
	}
	return d.search(ctx, name, episode, q.MaxSeasons)
}

// search 按拆开的名称搜索，episode 为要过滤的集号（nil 为不按集号过滤），供 Search 与 Match 共用。
func (d *DandanService) search(ctx context.Context, name catalog.ParsedName, episode *int, maxSeasons int) (DandanSearchResult, error) {
	query := fulltext.Query(name.Title)
	if query == "" {
		return DandanSearchResult{}, nil
	}
	rows, err := d.store.SearchSeasons(ctx, sqlc.SearchSeasonsParams{
		Query: query, Season: int32Ptr(name.Season), Episode: int32Ptr(episode), MaxRows: int32(maxSeasons + 1),
	})
	if err != nil {
		return DandanSearchResult{}, fmt.Errorf("search seasons: %w", err)
	}
	if len(rows) == 0 {
		return DandanSearchResult{}, nil
	}
	result := DandanSearchResult{HasMore: len(rows) > maxSeasons}
	rows = rows[:min(len(rows), maxSeasons)]

	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	episodes, err := d.listEpisodes(ctx, ids)
	if err != nil {
		return DandanSearchResult{}, err
	}
	result.Seasons = make([]DandanSeason, 0, len(rows))
	for _, r := range rows {
		s := seasonOf(r, episodes[r.ID])
		if episode != nil {
			s.Episodes = slices.DeleteFunc(s.Episodes, func(e DandanEpisode) bool { return e.Number != *episode })
			if len(s.Episodes) == 0 { // 查完季之后这一集被删除了，不返回，保证返回的季都带着这一集
				continue
			}
		}
		result.Seasons = append(result.Seasons, s)
	}
	return result, nil
}

// DandanMatchResult 识别的结果。Candidates 按可能性排序，每个是一季，Episodes 恰好是认出的那一集；
// Exact 为 true 时恰好一个候选，而且名称里的标题就是这部剧的标题或原名，可以直接采用。
// Parsed 是名称按 catalog.ParseName 认出的标题、季号、集号，Parsed.Episode 为 nil 时没有搜索。
type DandanMatchResult struct {
	Candidates []DandanSeason
	Exact      bool
	Parsed     catalog.ParsedName
}

// Match 按名称（match 的文件名）识别一集。名称里要写明集号（catalog.ParseName），否则没有候选；
// 其余与搜索相同：写明的季号只要这一季，有这一集的季都是候选，按搜索的排序，最多 maxCandidates 个。
// 候选唯一不算确定：名称里的标题可能只是剧名中的一段，标题与剧名或原名的词完全相同（fulltext.SameWords）才是 Exact。
func (d *DandanService) Match(ctx context.Context, name string, maxCandidates int) (DandanMatchResult, error) {
	parsed := catalog.ParseName(name)
	if parsed.Episode == nil {
		return DandanMatchResult{Parsed: parsed}, nil
	}
	result, err := d.search(ctx, parsed, parsed.Episode, maxCandidates)
	if err != nil {
		return DandanMatchResult{}, err
	}
	exact := len(result.Seasons) == 1 && slices.ContainsFunc(result.Seasons[0].Titles, func(t string) bool {
		return fulltext.SameWords(t, parsed.Title)
	})
	return DandanMatchResult{Candidates: result.Seasons, Exact: exact, Parsed: parsed}, nil
}

// Season 一季连同它的全部集，与 Search 返回的同一季相同；季不存在时 found 为 false，不是错误。不包事务：查完季后它被删除时，集列表读成空的。
func (d *DandanService) Season(ctx context.Context, id int64) (DandanSeason, bool, error) {
	row, err := d.store.GetSeason(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return DandanSeason{}, false, nil
	}
	if err != nil {
		return DandanSeason{}, false, fmt.Errorf("get season %d: %w", id, err)
	}
	episodes, err := d.listEpisodes(ctx, []int64{id})
	if err != nil {
		return DandanSeason{}, false, err
	}
	// GetSeason 的列与 SearchSeasons 相同，行可以直接转换；两边的列不一致时编译不通过
	return seasonOf(sqlc.SearchSeasonsRow(row), episodes[id]), true, nil
}

// listEpisodes 各季的全部集，按季 ID 分组，组内按集号升序。
func (d *DandanService) listEpisodes(ctx context.Context, seasonIDs []int64) (map[int64][]DandanEpisode, error) {
	rows, err := d.store.ListEpisodesBySeasons(ctx, seasonIDs)
	if err != nil {
		return nil, fmt.Errorf("list episodes of seasons: %w", err)
	}
	bySeason := make(map[int64][]DandanEpisode, len(seasonIDs)) // 查询已按集号排序
	for _, e := range rows {
		bySeason[e.SeasonID] = append(bySeason[e.SeasonID], DandanEpisode{ID: e.ID, Number: int(e.Number), Title: emptyIfNull(e.Title)})
	}
	return bySeason, nil
}

// seasonOf 由查出的一季（季号与所属剧的类型、剧名、原名、年份）和它的全部集组装一季，名称和类别按目录的规则得出。
func seasonOf(r sqlc.SearchSeasonsRow, episodes []DandanEpisode) DandanSeason {
	typ, n := catalog.SeriesType(r.Type), int(r.Number)
	titles := []string{r.Title}
	if r.OriginalTitle != nil {
		titles = append(titles, *r.OriginalTitle)
	}
	return DandanSeason{
		ID:           r.ID,
		Name:         catalog.SeasonName(typ, r.Title, n),
		Titles:       titles,
		Kind:         catalog.KindOf(typ, n),
		Year:         intPtr(r.Year),
		Episodes:     episodes,
		EpisodeCount: len(episodes),
	}
}

// Comments 一集的全部弹幕：读出这一集的绑定（失效的照常参与），再用一条查询取出这些绑定的全部弹幕，按绑定分组，
// 交给 danmaku.Merge 校正、跨源去重、排序。集不存在或没有绑定时返回空。播放时不向平台现取（不做 live 存储模式，见 ADR 0002）。
// 不包事务：所有绑定的弹幕一条 SELECT 读完，不会读到某次重新拉取的一半；查完绑定列表后某个绑定被删除时，它的弹幕读成空的。
func (d *DandanService) Comments(ctx context.Context, episodeID int64) ([]danmaku.Item, error) {
	bindings, err := d.store.ListBindingsByEpisode(ctx, episodeID)
	if err != nil {
		return nil, fmt.Errorf("list bindings of episode %d: %w", episodeID, err)
	}
	if len(bindings) == 0 {
		return nil, nil
	}
	tracks := make([]danmaku.Track, len(bindings))
	ids := make([]int64, len(bindings))
	for i, b := range bindings {
		platform, err := bindingPlatform(d.sources, b)
		if err != nil {
			return nil, err
		}
		tracks[i] = danmaku.Track{BindingID: b.ID, Platform: platform, Offset: b.Offset, Scale: b.Scale}
		ids[i] = b.ID
	}
	rows, err := d.store.ListDanmakuByBindings(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list danmaku of episode %d: %w", episodeID, err)
	}
	byBinding := make(map[int64][]danmaku.Danmaku, len(bindings))
	for _, r := range rows {
		byBinding[r.BindingID] = append(byBinding[r.BindingID],
			danmaku.Danmaku{TimeMs: r.TimeMs, Mode: danmaku.Mode(r.Mode), Color: uint32(r.Color), Text: r.Text, SourceID: r.SourceID})
	}
	for i := range tracks {
		tracks[i].Items = byBinding[tracks[i].BindingID]
	}
	return danmaku.Merge(tracks), nil
}
