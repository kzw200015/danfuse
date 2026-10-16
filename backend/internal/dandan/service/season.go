package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/dandan/dao"
	"github.com/kzw200015/danfuse/backend/internal/naming"
)

type Season struct {
	ID           int64
	Name         string   // 按 naming.SeasonName 拼
	Titles       []string // 所属剧的标题，有原名时加上原名；识别时用来判断文件名里的标题是不是这部剧
	Kind         naming.SeasonKind
	Year         *int32
	Episodes     []Episode // 按集号升序
	EpisodeCount int       // 这一季的总集数；按集号过滤时 Episodes 只有那一集，总集数不变
}

type Episode struct {
	ID     int64
	Number int
	Title  string // 可以为空
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
	return seasonOf(dao.SearchSeasonsRow(row), episodes[id]), true, nil
}

// seasonEpisodes 一季查出的集（按集号升序）与总集数。
type seasonEpisodes struct {
	list  []Episode
	count int
}

// listEpisodes 各季的集，按季 ID 分组；number 不为 nil 时只要这个集号的集，总集数不变。
func (d *Service) listEpisodes(ctx context.Context, seasonIDs []int64, number *int) (map[int64]seasonEpisodes, error) {
	rows, err := d.q.ListEpisodesBySeasons(ctx, dao.ListEpisodesBySeasonsParams{SeasonIds: seasonIDs, Number: int32Ptr(number)})
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
func seasonOf(r dao.SearchSeasonsRow, episodes seasonEpisodes) Season {
	typ, n := naming.SeriesType(r.Type), int(r.Number)
	titles := []string{r.Title}
	if r.OriginalTitle != nil {
		titles = append(titles, *r.OriginalTitle)
	}
	return Season{
		ID:           r.ID,
		Name:         naming.SeasonName(typ, r.Title, n),
		Titles:       titles,
		Kind:         naming.KindOf(typ, n),
		Year:         r.Year,
		Episodes:     episodes.list,
		EpisodeCount: episodes.count,
	}
}
