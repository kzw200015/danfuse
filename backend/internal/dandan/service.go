package dandan

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/blockword"
	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/catalog/naming"
	"github.com/kzw200015/danfuse/backend/internal/dandan/dandandb"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/fulltext"
)

// Service 弹弹 API 背后的查询：在目录里搜索季、按名称识别一集、按 ID 取季，读取一集所有绑定的弹幕。
// 返回的是系统内部的结构，不是弹弹play 的 JSON：协议参数的转换与格式化归 handler。
type Service struct {
	q            *dandandb.Queries
	bindings     *binding.Service   // 一集各绑定的弹幕
	blockedWords *blockword.Service // 取弹幕时要去掉的屏蔽词
}

func NewService(pool *pgxpool.Pool, bindings *binding.Service, blockedWords *blockword.Service) *Service {
	return &Service{q: dandandb.New(pool), bindings: bindings, blockedWords: blockedWords}
}

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

type Season struct {
	ID           int64
	Name         string   // 按 catalog.SeasonName 拼
	Titles       []string // 所属剧的标题，有原名时加上原名；识别时用来判断文件名里的标题是不是这部剧
	Kind         catalog.SeasonKind
	Year         *int32
	Episodes     []Episode // 按集号升序
	EpisodeCount int       // 这一季的总集数；按集号过滤时 Episodes 只有那一集，总集数不变
}

type Episode struct {
	ID     int64
	Number int
	Title  string // 可以为空
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
	rows, err := d.q.SearchSeasons(ctx, dandandb.SearchSeasonsParams{
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

// Season 一季连同它的全部集，与 Search 返回的同一季相同；季不存在时 found 为 false，不是错误。不包事务：查完季后它被删除时，集列表读成空的。
func (d *Service) Season(ctx context.Context, id int64) (Season, bool, error) {
	row, err := d.q.GetSeason(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Season{}, false, nil
	}
	if err != nil {
		return Season{}, false, fmt.Errorf("get season %d: %w", id, err)
	}
	episodes, err := d.listEpisodes(ctx, []int64{id}, nil)
	if err != nil {
		return Season{}, false, err
	}
	// GetSeason 的列与 SearchSeasons 相同，行可以直接转换；两边的列不一致时编译不通过
	return seasonOf(dandandb.SearchSeasonsRow(row), episodes[id]), true, nil
}

// seasonEpisodes 一季查出的集（按集号升序）与总集数。
type seasonEpisodes struct {
	list  []Episode
	count int
}

// listEpisodes 各季的集，按季 ID 分组；number 不为 nil 时只要这个集号的集，总集数不变。
func (d *Service) listEpisodes(ctx context.Context, seasonIDs []int64, number *int) (map[int64]seasonEpisodes, error) {
	rows, err := d.q.ListEpisodesBySeasons(ctx, dandandb.ListEpisodesBySeasonsParams{SeasonIds: seasonIDs, Number: int32Ptr(number)})
	if err != nil {
		return nil, fmt.Errorf("list episodes of seasons: %w", err)
	}
	bySeason := make(map[int64]seasonEpisodes, len(seasonIDs)) // 查询已按集号排序
	for _, e := range rows {
		s := bySeason[e.SeasonID]
		s.list = append(s.list, Episode{ID: e.ID, Number: int(e.Number), Title: emptyIfNull(e.Title)})
		s.count = int(e.EpisodeCount)
		bySeason[e.SeasonID] = s
	}
	return bySeason, nil
}

// seasonOf 由查出的一季（季号与所属剧的类型、剧名、原名、年份）和它的集组装一季，名称和类别按目录的规则得出。
func seasonOf(r dandandb.SearchSeasonsRow, episodes seasonEpisodes) Season {
	typ, n := catalog.SeriesType(r.Type), int(r.Number)
	titles := []string{r.Title}
	if r.OriginalTitle != nil {
		titles = append(titles, *r.OriginalTitle)
	}
	return Season{
		ID:           r.ID,
		Name:         catalog.SeasonName(typ, r.Title, n),
		Titles:       titles,
		Kind:         catalog.KindOf(typ, n),
		Year:         r.Year,
		Episodes:     episodes.list,
		EpisodeCount: episodes.count,
	}
}

// Comments 一集合并后的全部弹幕（danmaku.Merge），去掉了正文命中屏蔽词的。集不存在或没有绑定时返回空。
// 弹幕（binding.Service.EpisodeTracks）与屏蔽词同时读，屏蔽词不多占一次数据库往返的时间。
func (d *Service) Comments(ctx context.Context, episodeID int64) ([]danmaku.Item, error) {
	var (
		tracks    []danmaku.Track
		blocklist danmaku.Blocklist
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		tracks, err = d.bindings.EpisodeTracks(gctx, episodeID)
		return err
	})
	g.Go(func() (err error) {
		blocklist, err = d.blockedWords.Blocklist(gctx)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, nil
	}
	return danmaku.Merge(tracks, blocklist), nil
}

func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	return new(int32(*v))
}

// emptyIfNull null 读作空串。
func emptyIfNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
