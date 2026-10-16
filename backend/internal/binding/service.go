// Package binding 绑定：一集与一个弹幕源的对应关系。
//   - service.go：绑定的视图、改偏移与删除，以及包内共用的取、锁；
//   - link.go：贴链接创建（拉取弹幕源的全部弹幕落库）、重新拉取、失效与恢复；
//   - file.go：用弹幕文件建的绑定（创建、追加文件、重新解析）；
//   - danmaku_write.go：把弹幕写进绑定，链接、文件、槽位共用；danmaku_read.go：查看保存的弹幕、读取一集各绑定的弹幕；
//   - season_slot.go：季绑定在各集的槽位里建出的绑定（CreateLinkSlotInTx、CreateFileSlotInTx、DeleteSlotsInTx），
//     事务由 seasonbinding 开；建出的绑定不能单独删除、追加文件；
//   - scheduled_fetch.go：定时拉取。
package binding

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/binding/dao"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	errEpisodeNotFound = apierr.ErrNotFound.WithMessage("集不存在")
	errEpisodeDeleted  = apierr.ErrNotFound.WithMessage("这一集已被删除")
	errBindingExists   = apierr.ErrConflict.WithMessage("这一集已经绑定过这个弹幕源")
	errBindingNotFound = apierr.ErrNotFound.WithMessage("绑定不存在")
	errBindingDeleted  = apierr.ErrNotFound.WithMessage("绑定已被删除")
	errNotLinkBinding  = apierr.ErrBadRequest.WithMessage("用弹幕文件建的绑定不能重新拉取")
)

// errBuiltBinding 季绑定建出的绑定不能单独删除、追加文件，title 是建出它的季绑定的名称。
func errBuiltBinding(title string) error {
	return apierr.ErrConflict.WithMessage(fmt.Sprintf("这个绑定由季绑定「%s」建出，不能单独删除或追加文件；要改，去季面板编辑季绑定", title))
}

// 绑定的 kind：弹幕源的形态（见 docs/adr/0004）。
const (
	kindLink = "link" // 贴链接或补建出的，按适配器和 ref 能重新拉取
	kindFile = "file" // 上传的一组弹幕文件，没有适配器和 ref
)

// Service 绑定：贴链接创建，拉取弹幕源的全部弹幕落库；重新拉取、改偏移与删除。
// 用弹幕文件建的绑定（创建、追加文件、重新解析）见 file.go，季绑定的槽位见 season_slot.go。
// 拉取（网络请求）都在事务之外，拉完才开写入事务，写入事务的第一句锁住要写的行（创建时锁集，重新拉取时锁绑定）；
// 不加应用层的锁，并发靠行锁、外键级联和唯一约束。
type Service struct {
	pool    *pgxpool.Pool
	q       *dao.Queries
	sources *source.Registry
	logger  *slog.Logger
}

func NewService(pool *pgxpool.Pool, sources *source.Registry, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: dao.New(pool), sources: sources, logger: logger}
}

// View 绑定的 JSON。不对外暴露原始 ref。sourceUrl / sourceLabel：贴链接建的由适配器的
// Describe 生成；用弹幕文件建的没有链接，标签写明文件的份数（季绑定建出的为条目的文件份数）。
type View struct {
	ID           int64   `json:"id"`
	Kind         string  `json:"kind"`        // link | file
	Adapter      *string `json:"adapter"`     // 用弹幕文件建的为 null
	SourceURL    *string `json:"sourceUrl"`   // 用弹幕文件建的为 null
	SourceLabel  string  `json:"sourceLabel"` // 例如"B 站投稿 BV1xx411c7XX P2"、"弹幕文件 · 5 份"
	Title        string  `json:"title"`       // 弹幕源的标题；季绑定建出的文件绑定为条目名称
	Duration     *int32  `json:"duration"`    // 弹幕源视频的时长，秒；用弹幕文件建的没有时长，为 null
	Offset       float64 `json:"offset"`      // 秒，正数表示弹幕延后
	Status       string  `json:"status"`      // active | dead
	DanmakuCount int32   `json:"danmakuCount"`
	// ContentVersion 弹幕内容的版本：插入了新弹幕、或替换了全部弹幕时加 1，改偏移不变。管理界面据此刷新已打开的弹幕列表
	ContentVersion int32 `json:"contentVersion"`
	// MaxTimeMs 最晚一条弹幕的时间（毫秒，未校正），管理界面查看弹幕时作为拖动条的长度；没有弹幕时为 0
	MaxTimeMs     int32      `json:"maxTimeMs"`
	LastFetchedAt *time.Time `json:"lastFetchedAt"`
	// SeasonBindingID 建出这个绑定的季绑定；在集上手动建的为 null
	SeasonBindingID *int64 `json:"seasonBindingId"`
}

// ListBySeries 一部剧的全部绑定，按集 ID 分组，组内按创建顺序。剧详情用。
func (s *Service) ListBySeries(ctx context.Context, seriesID int64) (map[int64][]View, error) {
	rows, err := s.q.ListBindingsBySeries(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list bindings of series %d: %w", seriesID, err)
	}
	views := make(map[int64][]View) // 集 ID → 这一集的绑定
	for _, r := range rows {
		v, err := s.describe(r.Binding, r.EntryFileCount)
		if err != nil {
			return nil, err
		}
		views[r.Binding.EpisodeID] = append(views[r.Binding.EpisodeID], v)
	}
	return views, nil
}

// lockEpisode 写入事务里锁住一集到提交（FOR KEY SHARE），期间删不掉它；这一集已被删除时返回 gone。
func lockEpisode(ctx context.Context, q *dao.Queries, id int64, gone error) error {
	if _, err := q.LockEpisode(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gone
		}
		return fmt.Errorf("lock episode %d: %w", id, err)
	}
	return nil
}

// getBinding 取出绑定，不存在时返回 404"绑定不存在"。
func (s *Service) getBinding(ctx context.Context, id int64) (dao.Binding, error) {
	b, err := s.q.GetBinding(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dao.Binding{}, errBindingNotFound
		}
		return dao.Binding{}, fmt.Errorf("get binding %d: %w", id, err)
	}
	return b, nil
}

// lockBinding 重新拉取、追加文件、重新解析的写入事务的第一句：锁住这个绑定到提交，同一个绑定的写入排队执行。
// 绑定已被删除时返回 404"绑定已被删除"。
func lockBinding(ctx context.Context, q *dao.Queries, id int64) error {
	if _, err := q.LockBinding(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errBindingDeleted
		}
		return fmt.Errorf("lock binding %d: %w", id, err)
	}
	return nil
}

// UpdateOffset 改偏移（秒，正数表示弹幕延后），单条语句，content_version 不变。取值范围由调用方校验。
func (s *Service) UpdateOffset(ctx context.Context, id int64, offset float64) (View, error) {
	b, err := s.q.UpdateBindingOffset(ctx, dao.UpdateBindingOffsetParams{ID: id, Offset: offset})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, errBindingNotFound
		}
		return View{}, fmt.Errorf("update offset of binding %d: %w", id, err)
	}
	return s.view(ctx, b)
}

// Delete 删除绑定，它的弹幕随外键级联删除。单条语句；进行中的拉取写回时会发现绑定已被删除。
// 季绑定建出的绑定不删，返回 409（见 checkNotBuilt）。
func (s *Service) Delete(ctx context.Context, id int64) error {
	n, err := s.q.DeleteBinding(ctx, id)
	if err != nil {
		return fmt.Errorf("delete binding %d: %w", id, err)
	}
	if n > 0 {
		return nil
	}
	b, err := s.getBinding(ctx, id)
	if err != nil {
		return err
	}
	return s.checkNotBuilt(ctx, b)
}

// checkNotBuilt 季绑定建出的绑定（随季绑定的表单增删）不能单独删除、追加文件：返回 409，提示写明建出它的季绑定。
func (s *Service) checkNotBuilt(ctx context.Context, b dao.Binding) error {
	if b.SeasonBindingID == nil {
		return nil
	}
	title, err := s.q.GetSeasonBindingTitle(ctx, *b.SeasonBindingID)
	if errors.Is(err, pgx.ErrNoRows) { // 季绑定刚被删除，它建出的绑定随之删除
		return errBindingNotFound
	}
	if err != nil {
		return fmt.Errorf("get season binding %d of binding %d: %w", *b.SeasonBindingID, b.ID, err)
	}
	return errBuiltBinding(title)
}

// view 绑定的 JSON（见 describe）：季绑定建出的文件绑定另查条目的文件份数。
func (s *Service) view(ctx context.Context, b dao.Binding) (View, error) {
	var entryFiles int32
	if b.EntryID != nil {
		var err error
		if entryFiles, err = s.q.CountEntryFiles(ctx, *b.EntryID); err != nil {
			return View{}, fmt.Errorf("count entry files of binding %d: %w", b.ID, err)
		}
	}
	return s.describe(b, entryFiles)
}

// describe 绑定的 JSON：贴链接建的，弹幕源的链接和标签交给它的适配器生成；用弹幕文件建的，标签写明文件的份数，
// 季绑定建出的文件绑定的文件在条目上，份数为 entryFiles。
func (s *Service) describe(b dao.Binding, entryFiles int32) (View, error) {
	v := View{
		ID:              b.ID,
		Kind:            b.Kind,
		Adapter:         b.Adapter,
		Title:           b.Title,
		Duration:        b.Duration,
		Offset:          b.Offset,
		Status:          b.Status,
		DanmakuCount:    b.DanmakuCount,
		ContentVersion:  b.ContentVersion,
		MaxTimeMs:       b.MaxTimeMs,
		LastFetchedAt:   b.LastFetchedAt,
		SeasonBindingID: b.SeasonBindingID,
	}
	if b.Kind == kindFile {
		files := b.FileCount
		if b.EntryID != nil {
			files = entryFiles
		}
		v.SourceLabel = fmt.Sprintf("弹幕文件 · %d 份", files)
		return v, nil
	}
	adapter, err := s.linkAdapter(b)
	if err != nil {
		return View{}, err
	}
	d, err := adapter.Describe(b.Ref)
	if err != nil {
		return View{}, fmt.Errorf("describe binding %d: %w", b.ID, err)
	}
	v.SourceURL, v.SourceLabel = &d.URL, d.Label
	return v, nil
}

// platform 绑定的弹幕所在的平台：贴链接建的取自适配器；弹幕文件里的弹幕不属于任何平台（见 docs/adr/0004）。
func (s *Service) platform(b dao.Binding) (danmaku.Platform, error) {
	if b.Kind == kindFile {
		return danmaku.PlatformNone, nil
	}
	adapter, err := s.linkAdapter(b)
	if err != nil {
		return "", err
	}
	return adapter.Platform(), nil
}

// emptyIfNull null 读作空串。
func emptyIfNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
