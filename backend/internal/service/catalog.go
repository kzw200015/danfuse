package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/repository"
)

var (
	errSeriesNotFound = errcode.ErrNotFound.WithMessage("剧不存在")
	errImageNotFound  = errcode.ErrNotFound.WithMessage("图片不存在")
)

// CatalogService 管理界面浏览目录。
type CatalogService struct {
	store repository.Store
}

func NewCatalogService(store repository.Store) *CatalogService {
	return &CatalogService{store: store}
}

// ListSeries 全部剧连同海报的图片 ID、季数、集数，不分页：自用规模在几百到一两千部。筛选和排序由前端做。
func (s *CatalogService) ListSeries(ctx context.Context) ([]repository.ListSeriesRow, error) {
	series, err := s.store.ListSeries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list series: %w", err)
	}
	return series, nil
}

// SeriesDetail 一部剧的完整子树：季按季号、集按集号排序。
type SeriesDetail struct {
	ID            int64          `json:"id"`
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	OriginalTitle *string        `json:"originalTitle"`
	Year          *int32         `json:"year"`
	PosterImageID *int64         `json:"posterImageId"` // 没有海报时为 null
	Seasons       []SeasonDetail `json:"seasons"`
}

type SeasonDetail struct {
	ID       int64           `json:"id"`
	Number   int32           `json:"number"`
	Title    *string         `json:"title"`
	Episodes []EpisodeDetail `json:"episodes"`
}

type EpisodeDetail struct {
	ID       int64   `json:"id"`
	Number   int32   `json:"number"`
	Title    *string `json:"title"`
	Duration *int32  `json:"duration"` // 秒
}

// GetSeries 一部剧的完整子树。剧、季、集分开查询，在 Go 里组装；剧不存在时返回 404。
// 几次查询不在同一个快照里：查完季之后同步新增的季，它的集会出现在集的查询结果里，组装时丢弃，下次加载就完整了。
func (s *CatalogService) GetSeries(ctx context.Context, id int64) (SeriesDetail, error) {
	series, err := s.store.GetSeries(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SeriesDetail{}, errSeriesNotFound
		}
		return SeriesDetail{}, fmt.Errorf("get series %d: %w", id, err)
	}
	seasons, err := s.store.ListSeasonsBySeries(ctx, id)
	if err != nil {
		return SeriesDetail{}, fmt.Errorf("list seasons of series %d: %w", id, err)
	}
	episodes, err := s.store.ListEpisodesBySeries(ctx, id)
	if err != nil {
		return SeriesDetail{}, fmt.Errorf("list episodes of series %d: %w", id, err)
	}

	detail := SeriesDetail{
		ID:            series.ID,
		Type:          series.Type,
		Title:         series.Title,
		OriginalTitle: series.OriginalTitle,
		Year:          series.Year,
		PosterImageID: series.PosterImageID,
		Seasons:       make([]SeasonDetail, len(seasons)),
	}
	seasonIndex := make(map[int64]int, len(seasons)) // 季 ID → 在 detail.Seasons 里的下标
	for i, se := range seasons {
		detail.Seasons[i] = SeasonDetail{ID: se.ID, Number: se.Number, Title: se.Title, Episodes: []EpisodeDetail{}}
		seasonIndex[se.ID] = i
	}
	for _, e := range episodes {
		i, ok := seasonIndex[e.SeasonID]
		if !ok {
			continue
		}
		detail.Seasons[i].Episodes = append(detail.Seasons[i].Episodes, EpisodeDetail{
			ID:       e.ID,
			Number:   e.Number,
			Title:    e.Title,
			Duration: e.Duration,
		})
	}
	return detail, nil
}

// GetImage 一张图片的原始字节与 content-type；不存在时返回 404。
func (s *CatalogService) GetImage(ctx context.Context, id int64) (repository.GetImageRow, error) {
	img, err := s.store.GetImage(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.GetImageRow{}, errImageNotFound
		}
		return repository.GetImageRow{}, fmt.Errorf("get image %d: %w", id, err)
	}
	return img, nil
}
