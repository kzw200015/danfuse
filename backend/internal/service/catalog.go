package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	errSeriesNotFound  = errcode.ErrNotFound.WithMessage("剧不存在")
	errSeasonNotFound  = errcode.ErrNotFound.WithMessage("季不存在")
	errEpisodeNotFound = errcode.ErrNotFound.WithMessage("集不存在")
	errImageNotFound   = errcode.ErrNotFound.WithMessage("图片不存在")
)

// CatalogService 管理界面浏览目录，以及手动删除剧、季、集。
//
// 删除用来清理目录源里已经没有的条目，下级的季、集、绑定和弹幕随外键级联删除。同步进行中也能删除，不加应用层的锁：
// 同步正在写这部剧时，删除等它的事务提交，再连同刚写入的内容一起删掉；目录源里还在的条目，之后的同步（包括正在进行的这次）
// 按自然键找不到它，会用新 ID 重新建出来，绑定不会恢复。
type CatalogService struct {
	store   repository.Store
	sources *source.Registry // 剧详情里绑定的弹幕源链接和标签由适配器生成
}

func NewCatalogService(store repository.Store, sources *source.Registry) *CatalogService {
	return &CatalogService{store: store, sources: sources}
}

// ListSeries 全部剧连同海报的图片 ID、季数、集数与绑定统计，不分页：自用规模在几百到一两千部。筛选和排序由前端做。
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
	ID       int64         `json:"id"`
	Number   int32         `json:"number"`
	Title    *string       `json:"title"`
	Duration *int32        `json:"duration"` // 秒
	Bindings []BindingView `json:"bindings"` // 按创建顺序
}

// GetSeries 一部剧的完整子树：季 → 集 → 绑定。剧、季、集、绑定分开查询，在 Go 里组装；剧不存在时返回 404。
// 几次查询不在同一个快照里：查完季之后同步新增的季，它的集会出现在集的查询结果里；查完集之后新增的集，
// 它的绑定会出现在绑定的查询结果里。组装时都丢弃，下次加载就完整了。
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
	bindings, err := s.store.ListBindingsBySeries(ctx, id)
	if err != nil {
		return SeriesDetail{}, fmt.Errorf("list bindings of series %d: %w", id, err)
	}
	views := make(map[int64][]BindingView) // 集 ID → 这一集的绑定
	for _, b := range bindings {
		v, err := bindingView(s.sources, b)
		if err != nil {
			return SeriesDetail{}, err
		}
		views[b.EpisodeID] = append(views[b.EpisodeID], v)
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
			Bindings: append([]BindingView{}, views[e.ID]...), // 没有绑定时输出 []
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

// DeleteSeries 删除一部剧，连同它的海报：同一个事务里先删剧、再删图。剧不存在时返回 404。
func (s *CatalogService) DeleteSeries(ctx context.Context, id int64) error {
	return s.store.ExecTx(ctx, func(q repository.Querier) error {
		posterID, err := q.DeleteSeries(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errSeriesNotFound
			}
			return fmt.Errorf("delete series %d: %w", id, err)
		}
		if posterID == nil {
			return nil
		}
		if err := q.DeleteImage(ctx, *posterID); err != nil {
			return fmt.Errorf("delete poster %d of series %d: %w", *posterID, id, err)
		}
		return nil
	})
}

// DeleteSeason 删除一季，单条语句。季不存在时返回 404。
func (s *CatalogService) DeleteSeason(ctx context.Context, id int64) error {
	n, err := s.store.DeleteSeason(ctx, id)
	if err != nil {
		return fmt.Errorf("delete season %d: %w", id, err)
	}
	if n == 0 {
		return errSeasonNotFound
	}
	return nil
}

// DeleteEpisode 删除一集，单条语句。集不存在时返回 404。
func (s *CatalogService) DeleteEpisode(ctx context.Context, id int64) error {
	n, err := s.store.DeleteEpisode(ctx, id)
	if err != nil {
		return fmt.Errorf("delete episode %d: %w", id, err)
	}
	if n == 0 {
		return errEpisodeNotFound
	}
	return nil
}
