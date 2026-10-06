package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/pkg/apierr"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	errEpisodeDeleted  = apierr.ErrNotFound.WithMessage("这一集已被删除")
	errBindingExists   = apierr.ErrConflict.WithMessage("这一集已经绑定过这个弹幕源")
	errBindingNotFound = apierr.ErrNotFound.WithMessage("绑定不存在")
	errBindingDeleted  = apierr.ErrNotFound.WithMessage("绑定已被删除")
)

// BindingService 绑定：贴链接创建，拉取弹幕源的全部弹幕落库；重新拉取、改偏移与删除。
// 拉取（网络请求）都在事务之外，拉完才开写入事务，写入事务的第一句锁住要写的行（创建时锁集，重新拉取时锁绑定）；
// 不加应用层的锁，并发靠行锁、外键级联和唯一约束。
type BindingService struct {
	store   *repository.Store
	sources *source.Registry
	logger  *slog.Logger
}

func NewBindingService(store *repository.Store, sources *source.Registry, logger *slog.Logger) *BindingService {
	return &BindingService{store: store, sources: sources, logger: logger}
}

// BindingView 绑定的 JSON。不对外暴露原始 ref 和 contentVersion，sourceUrl / sourceLabel 由适配器的 Describe 生成。
type BindingView struct {
	ID            int64      `json:"id"`
	Adapter       string     `json:"adapter"`
	SourceURL     string     `json:"sourceUrl"`
	SourceLabel   string     `json:"sourceLabel"`
	Title         string     `json:"title"`    // 弹幕源的标题
	Duration      int32      `json:"duration"` // 弹幕源视频的时长，秒
	Offset        float64    `json:"offset"`   // 秒，正数表示弹幕延后
	Status        string     `json:"status"`   // active | dead
	DanmakuCount  int32      `json:"danmakuCount"`
	LastFetchedAt *time.Time `json:"lastFetchedAt"`
	// SeasonBindingID 建出这个绑定的季绑定；手动贴链接建的、或季绑定已被删除的为 null
	SeasonBindingID *int64 `json:"seasonBindingId"`
}

// Create 贴链接创建绑定：解析链接 → 确认这一集存在（404）→ 查重（409）→ 拉取 → 写入。拉取失败就不创建。
// 拉取期间这一集被删除时返回 404"这一集已被删除"；同时两次给同一集贴同一个弹幕源时，后提交的撞上唯一约束返回 409。
func (s *BindingService) Create(ctx context.Context, episodeID int64, link string) (BindingView, error) {
	// 解析短链也要联网：解析链接与拉取共用 fetchTimeout，从解析开始计时；查库和写入事务仍用 ctx
	netCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	adapter, ref, err := s.sources.ParseLink(netCtx, link)
	if err != nil {
		return BindingView{}, sourceAPIError(err)
	}
	switch exists, err := s.store.EpisodeExists(ctx, episodeID); {
	case err != nil:
		return BindingView{}, fmt.Errorf("check episode %d: %w", episodeID, err)
	case !exists:
		return BindingView{}, errEpisodeNotFound
	}
	// 只是省掉一次注定 409 的拉取；并发时以写入事务里的唯一约束为准
	key := repository.BindingExistsParams{EpisodeID: episodeID, Adapter: adapter.ID(), Ref: ref}
	switch bound, err := s.store.BindingExists(ctx, key); {
	case err != nil:
		return BindingView{}, fmt.Errorf("check binding of episode %d: %w", episodeID, err)
	case bound:
		return BindingView{}, errBindingExists
	}

	fetched, err := fetch(netCtx, adapter, ref)
	if err != nil {
		return BindingView{}, sourceAPIError(err)
	}
	fetchedAt := time.Now()

	var (
		binding repository.Binding
		added   int64
	)
	err = s.store.ExecTx(ctx, func(q *repository.Queries) error {
		// 锁住这一集到提交：之后的删除要等这个事务提交，再连同绑定和弹幕一起删掉
		if _, err := q.LockEpisode(ctx, episodeID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errEpisodeDeleted
			}
			return fmt.Errorf("lock episode %d: %w", episodeID, err)
		}
		id, err := q.InsertBinding(ctx, repository.InsertBindingParams{
			EpisodeID: episodeID,
			Adapter:   adapter.ID(),
			Ref:       ref,
			Title:     fetched.Title,
			Duration:  int32(fetched.Duration),
		})
		if err != nil {
			if database.IsUniqueViolation(err) {
				return errBindingExists
			}
			return fmt.Errorf("insert binding of episode %d: %w", episodeID, err)
		}
		binding, added, err = saveFetched(ctx, q, id, fetched, false, fetchedAt)
		return err
	})
	if err != nil {
		return BindingView{}, err
	}
	s.logFetched(ctx, binding, fetched, added)
	return bindingView(s.sources, binding)
}

// Refetch 重新拉取一个绑定的全部弹幕，返回更新后的绑定和新增条数，见 refetch；适配器的错误转成管理 API 的错误。
func (s *BindingService) Refetch(ctx context.Context, id int64, replace bool) (BindingView, int64, error) {
	view, added, err := s.refetch(ctx, id, replace)
	return view, added, sourceAPIError(err)
}

// refetch Refetch 的实现，适配器的错误原样返回（*source.Error）。不依赖 HTTP 请求，
// 追更的自动重新拉取（SeasonBindingService.refetchRecent）也复用它，按 Kind 决定是否停下这一轮。
//   - replace 为 false（重新拉取）：只插入新弹幕，从不删除，平台上已经删掉的弹幕继续保留。
//   - replace 为 true（清空后重新拉取，即管理 API 的 clear）：拉取成功后，在同一个事务里删掉这个绑定的全部弹幕、
//     写入这次的结果；新增条数为这次的总条数。
//
// 拉取失败时什么都不改，只有弹幕源不存在（NotFound）时把绑定标为失效，已保存的弹幕保留；失效的绑定拉取成功后恢复正常。
// 拉取期间绑定被删除时返回 404"绑定已被删除"，拉取结果丢弃。
func (s *BindingService) refetch(ctx context.Context, id int64, replace bool) (BindingView, int64, error) {
	b, err := s.store.GetBinding(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BindingView{}, 0, errBindingNotFound
		}
		return BindingView{}, 0, fmt.Errorf("get binding %d: %w", id, err)
	}
	adapter, err := s.sources.Get(b.Adapter)
	if err != nil {
		return BindingView{}, 0, fmt.Errorf("binding %d: %w", id, err)
	}

	fetched, err := fetch(ctx, adapter, b.Ref)
	if err != nil {
		if srcErr, ok := errors.AsType[*source.Error](err); ok && srcErr.Kind == source.NotFound {
			if err := s.markDead(ctx, b, srcErr); err != nil {
				return BindingView{}, 0, err
			}
		}
		return BindingView{}, 0, err
	}
	fetchedAt := time.Now()

	var added int64
	err = s.store.ExecTx(ctx, func(q *repository.Queries) error {
		if err := lockBinding(ctx, q, id); err != nil {
			return err
		}
		var err error
		b, added, err = saveFetched(ctx, q, id, fetched, replace, fetchedAt)
		return err
	})
	if err != nil {
		return BindingView{}, 0, err
	}
	s.logFetched(ctx, b, fetched, added)
	view, err := bindingView(s.sources, b)
	return view, added, err
}

// markDead 重新拉取时弹幕源已不存在：把绑定标为失效，已保存的弹幕保留。
// 成功后记一条 info 日志，连同适配器给的原因：422 不经过 errorHandler 的日志，追更自动重新拉取时也能看出绑定失效了。
func (s *BindingService) markDead(ctx context.Context, b repository.Binding, reason error) error {
	err := s.store.ExecTx(ctx, func(q *repository.Queries) error {
		if err := lockBinding(ctx, q, b.ID); err != nil {
			return err
		}
		if err := q.MarkBindingDead(ctx, repository.MarkBindingDeadParams{ID: b.ID, FetchedAt: time.Now()}); err != nil {
			return fmt.Errorf("mark binding %d dead: %w", b.ID, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "binding marked dead",
		slog.Int64("binding_id", b.ID), slog.String("adapter", b.Adapter), slog.String("reason", reason.Error()))
	return nil
}

// lockBinding 重新拉取的写入事务的第一句：锁住这个绑定到提交，同一个绑定的写入排队执行。
// 绑定已被删除时返回 404"绑定已被删除"。
func lockBinding(ctx context.Context, q *repository.Queries, id int64) error {
	if _, err := q.LockBinding(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errBindingDeleted
		}
		return fmt.Errorf("lock binding %d: %w", id, err)
	}
	return nil
}

// UpdateOffset 改偏移（秒，正数表示弹幕延后），单条语句，content_version 不变。取值范围由调用方校验。
func (s *BindingService) UpdateOffset(ctx context.Context, id int64, offset float64) (BindingView, error) {
	b, err := s.store.UpdateBindingOffset(ctx, repository.UpdateBindingOffsetParams{ID: id, Offset: offset})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BindingView{}, errBindingNotFound
		}
		return BindingView{}, fmt.Errorf("update offset of binding %d: %w", id, err)
	}
	return bindingView(s.sources, b)
}

// Delete 删除绑定，它的弹幕随外键级联删除。单条语句；进行中的拉取写回时会发现绑定已被删除。
func (s *BindingService) Delete(ctx context.Context, id int64) error {
	n, err := s.store.DeleteBinding(ctx, id)
	if err != nil {
		return fmt.Errorf("delete binding %d: %w", id, err)
	}
	if n == 0 {
		return errBindingNotFound
	}
	return nil
}

// saveFetched 在写入事务里保存一次拉取的结果：replace 时先删掉这个绑定的全部弹幕；
// 插入弹幕（按原始 ID 去重，已有的跳过），再更新绑定的计数、content_version、标题、时长与拉取时间 fetchedAt。
// 调用方已在同一个事务里锁住或刚插入这个绑定。返回更新后的绑定和新增条数（replace 时即这次的总条数）。
//
// 拉取时间取自应用的时钟（拉取完成时的 time.Now()），不用数据库的 now()：追更按它判断自动重新拉取是否已满 24 小时，
// 与上次检查时间用同一个时钟，测试里也能用假时间推进。
func saveFetched(ctx context.Context, q *repository.Queries, bindingID int64, f source.Fetched, replace bool, fetchedAt time.Time) (repository.Binding, int64, error) {
	if replace {
		if err := q.DeleteDanmaku(ctx, bindingID); err != nil {
			return repository.Binding{}, 0, fmt.Errorf("delete danmaku of binding %d: %w", bindingID, err)
		}
	}
	p := repository.InsertDanmakuParams{
		BindingID: bindingID,
		SourceIds: make([]int64, len(f.Danmaku)),
		TimeMs:    make([]int32, len(f.Danmaku)),
		Modes:     make([]int16, len(f.Danmaku)),
		Colors:    make([]int32, len(f.Danmaku)),
		Texts:     make([]string, len(f.Danmaku)),
	}
	for i, d := range f.Danmaku {
		p.SourceIds[i] = d.SourceID
		p.TimeMs[i] = d.TimeMs
		p.Modes[i] = int16(d.Mode)
		p.Colors[i] = int32(d.Color)
		p.Texts[i] = d.Text
	}
	added, err := q.InsertDanmaku(ctx, p)
	if err != nil {
		return repository.Binding{}, 0, fmt.Errorf("insert danmaku of binding %d: %w", bindingID, err)
	}
	b, err := q.RecordFetch(ctx, repository.RecordFetchParams{
		ID:        bindingID,
		Replace:   replace,
		Added:     int32(added),
		Title:     f.Title,
		Duration:  int32(f.Duration),
		FetchedAt: fetchedAt,
	})
	if err != nil {
		return repository.Binding{}, 0, fmt.Errorf("record fetch of binding %d: %w", bindingID, err)
	}
	return b, added, nil
}

// logFetched 每次拉取结束记一条 info 日志：适配器自己的统计加上新增条数。
func (s *BindingService) logFetched(ctx context.Context, b repository.Binding, f source.Fetched, added int64) {
	attrs := []slog.Attr{slog.Int64("binding_id", b.ID), slog.String("adapter", b.Adapter)}
	attrs = append(attrs, f.LogAttrs...)
	attrs = append(attrs, slog.Int64("added", added))
	s.logger.LogAttrs(ctx, slog.LevelInfo, "danmaku fetched", attrs...)
}

// bindingView 绑定的 JSON，弹幕源的链接和标签交给它的适配器生成。
func bindingView(sources *source.Registry, b repository.Binding) (BindingView, error) {
	adapter, err := sources.Get(b.Adapter)
	if err != nil {
		return BindingView{}, fmt.Errorf("binding %d: %w", b.ID, err)
	}
	d, err := adapter.Describe(b.Ref)
	if err != nil {
		return BindingView{}, fmt.Errorf("describe binding %d: %w", b.ID, err)
	}
	return BindingView{
		ID:              b.ID,
		Adapter:         b.Adapter,
		SourceURL:       d.URL,
		SourceLabel:     d.Label,
		Title:           b.Title,
		Duration:        b.Duration,
		Offset:          b.Offset,
		Status:          b.Status,
		DanmakuCount:    b.DanmakuCount,
		LastFetchedAt:   b.LastFetchedAt,
		SeasonBindingID: b.SeasonBindingID,
	}, nil
}
