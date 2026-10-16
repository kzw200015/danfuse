package binding

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/binding/bindingdb"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// SourceAPIError 把适配器的 *source.Error 转成管理 API 的错误：InvalidLink 为 400，NotFound 为 422，其余为 502；
// 提示用适配器写的 Message，底层原因 Err 只进日志（不再包一层 *source.Error，日志里提示不重复）。
// 其他错误原样返回，按服务器内部错误处理。转换后取不出 Kind，要按 Kind 分支的调用方（补建、定时拉取）用转换之前的错误。
// 季绑定的预览与创建也用它。
func SourceAPIError(err error) error {
	srcErr, ok := errors.AsType[*source.Error](err)
	if !ok {
		return err
	}
	base := apierr.ErrBadGateway
	switch srcErr.Kind {
	case source.InvalidLink:
		base = apierr.ErrBadRequest
	case source.NotFound:
		base = apierr.ErrUnprocessable
	}
	return base.WithMessage(srcErr.Message).Wrap(srcErr.Err)
}

// Create 贴链接创建绑定：解析链接 → 确认这一集存在（404）→ 查重（409）→ 拉取 → 写入。拉取失败就不创建。
// 拉取期间这一集被删除时返回 404"这一集已被删除"；同时两次给同一集贴同一个弹幕源时，后提交的撞上唯一约束返回 409。
func (s *Service) Create(ctx context.Context, episodeID int64, link string) (View, error) {
	// 解析短链也要联网：解析链接与拉取共用 source.FetchTimeout，从解析开始计时；查库和写入事务仍用 ctx
	netCtx, cancel := context.WithTimeout(ctx, source.FetchTimeout)
	defer cancel()
	adapter, ref, err := s.sources.ParseLink(netCtx, link)
	if err != nil {
		return View{}, SourceAPIError(err)
	}
	switch exists, err := s.q.EpisodeExists(ctx, episodeID); {
	case err != nil:
		return View{}, fmt.Errorf("check episode %d: %w", episodeID, err)
	case !exists:
		return View{}, errEpisodeNotFound
	}
	// 只是省掉一次注定 409 的拉取；并发时以写入事务里的唯一约束为准
	switch bound, err := s.q.BindingExists(ctx, bindingdb.BindingExistsParams{EpisodeID: episodeID, Adapter: adapter.ID(), Ref: ref}); {
	case err != nil:
		return View{}, fmt.Errorf("check binding of episode %d: %w", episodeID, err)
	case bound:
		return View{}, errBindingExists
	}

	fetched, err := adapter.Fetch(netCtx, ref)
	if err != nil {
		return View{}, SourceAPIError(err)
	}
	fetchedAt := time.Now()

	var (
		binding bindingdb.Binding
		added   int64
	)
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		binding, added, err = insertLinkBinding(ctx, s.q.WithTx(tx), bindingdb.InsertLinkBindingParams{
			EpisodeID: episodeID,
			Adapter:   adapter.ID(),
			Ref:       ref,
			Title:     fetched.Title,
			Duration:  int32(fetched.Duration),
			CreatedAt: fetchedAt,
		}, fetched, errEpisodeDeleted)
		return err
	})
	if errors.Is(err, ErrSourceBound) {
		return View{}, errBindingExists
	}
	if err != nil {
		return View{}, err
	}
	s.logFetched(ctx, binding, fetched, added)
	return s.view(ctx, binding)
}

// Saved 写入一次拉取的结果，事务提交之后交给 LogFetched 记日志。
type Saved struct {
	Added   int64 // 新增的弹幕条数
	binding bindingdb.Binding
	fetched source.Fetched
}

// insertLinkBinding 在写入事务里建出一个链接绑定：锁住集到提交（之后的删除要等这个事务提交，再连同绑定和弹幕一起删掉；
// 集已被删除时返回 gone），插入绑定（集上已有同一个弹幕源时返回 ErrSourceBound），写入拉取的结果。
// 返回写入后的绑定和新增条数。
func insertLinkBinding(ctx context.Context, q *bindingdb.Queries, p bindingdb.InsertLinkBindingParams, fetched source.Fetched, gone error) (bindingdb.Binding, int64, error) {
	if err := lockEpisode(ctx, q, p.EpisodeID, gone); err != nil {
		return bindingdb.Binding{}, 0, err
	}
	id, err := q.InsertLinkBinding(ctx, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return bindingdb.Binding{}, 0, ErrSourceBound
	}
	if err != nil {
		return bindingdb.Binding{}, 0, fmt.Errorf("insert binding of episode %d: %w", p.EpisodeID, err)
	}
	return saveFetched(ctx, q, id, fetched, false, p.CreatedAt)
}

// LogFetched 季绑定建出的链接绑定写入提交之后记一条 "danmaku fetched"，与其他拉取的日志相同。
func (s *Service) LogFetched(ctx context.Context, saved Saved) {
	s.logFetched(ctx, saved.binding, saved.fetched, saved.Added)
}

// Refetch 重新拉取一个绑定的全部弹幕，返回更新后的绑定和新增条数，见 refetch；适配器的错误转成管理 API 的错误。
func (s *Service) Refetch(ctx context.Context, id int64, replace bool) (View, int64, error) {
	view, added, err := s.refetch(ctx, id, replace)
	return view, added, SourceAPIError(err)
}

// refetch Refetch 的实现，适配器的错误原样返回（*source.Error）。不依赖 HTTP 请求，
// 定时拉取（ScheduledFetchService）也复用它，按 Kind 决定是否停下这一轮。
//   - replace 为 false（重新拉取）：只插入新弹幕，从不删除，平台上已经删掉的弹幕继续保留。
//   - replace 为 true（清空后重新拉取，即管理 API 的 clear）：拉取成功后，在同一个事务里删掉这个绑定的全部弹幕、
//     写入这次的结果；新增条数为这次的总条数。
//
// 拉取失败时只记下尝试拉取的时间（定时拉取等满一个间隔再试）；弹幕源不存在（NotFound）时把绑定标为失效，已保存的弹幕保留；
// 失效的绑定拉取成功后恢复正常。
// 拉取期间绑定被删除时返回 404"绑定已被删除"，拉取结果丢弃。用弹幕文件建的绑定不能重新拉取（400）。
func (s *Service) refetch(ctx context.Context, id int64, replace bool) (View, int64, error) {
	b, err := s.getBinding(ctx, id)
	if err != nil {
		return View{}, 0, err
	}
	if b.Kind != kindLink {
		return View{}, 0, errNotLinkBinding
	}
	adapter, err := s.linkAdapter(b)
	if err != nil {
		return View{}, 0, err
	}

	fetchCtx, cancel := context.WithTimeout(ctx, source.FetchTimeout) // 拉完才开写入事务
	fetched, err := adapter.Fetch(fetchCtx, b.Ref)
	cancel()
	if err != nil {
		switch srcErr, ok := errors.AsType[*source.Error](err); {
		case !ok || ctx.Err() != nil: // 服务器内部错误、关闭服务：不算一次尝试
		case srcErr.Kind == source.NotFound:
			if err := s.markDead(ctx, b, srcErr); err != nil {
				return View{}, 0, err
			}
		default:
			if err := s.recordFetchAttempt(ctx, b.ID); err != nil {
				return View{}, 0, err
			}
		}
		return View{}, 0, err
	}
	fetchedAt := time.Now()

	var added int64
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := lockBinding(ctx, q, id); err != nil {
			return err
		}
		var err error
		b, added, err = saveFetched(ctx, q, id, fetched, replace, fetchedAt)
		return err
	})
	if err != nil {
		return View{}, 0, err
	}
	s.logFetched(ctx, b, fetched, added)
	view, err := s.view(ctx, b)
	return view, added, err
}

// markDead 重新拉取时弹幕源已不存在：把绑定标为失效，已保存的弹幕保留。
// 成功后记一条 info 日志，连同适配器给的原因：422 不经过 errorHandler 的日志，定时拉取时也能看出绑定失效了。
func (s *Service) markDead(ctx context.Context, b bindingdb.Binding, reason error) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := lockBinding(ctx, q, b.ID); err != nil {
			return err
		}
		if err := q.MarkBindingDead(ctx, bindingdb.MarkBindingDeadParams{ID: b.ID, FetchedAt: time.Now()}); err != nil {
			return fmt.Errorf("mark binding %d dead: %w", b.ID, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "binding marked dead",
		slog.Int64("binding_id", b.ID), slog.String("adapter", emptyIfNull(b.Adapter)), slog.String("reason", reason.Error()))
	return nil
}

// recordFetchAttempt 拉取失败（弹幕源不存在之外）时记下尝试拉取的时间，单条语句；绑定已被删除时什么都不做。
func (s *Service) recordFetchAttempt(ctx context.Context, id int64) error {
	if err := s.q.RecordFetchAttempt(ctx, bindingdb.RecordFetchAttemptParams{ID: id, AttemptedAt: time.Now()}); err != nil {
		return fmt.Errorf("record fetch attempt of binding %d: %w", id, err)
	}
	return nil
}

// logFetched 每次拉取结束记一条 info 日志：适配器自己的统计加上新增条数和拉取后的总条数；
// 季绑定建出的绑定另记 season_binding_id，能和 "backfill finished" 对上。
func (s *Service) logFetched(ctx context.Context, b bindingdb.Binding, f source.Fetched, added int64) {
	attrs := []slog.Attr{slog.Int64("binding_id", b.ID), slog.Int64("episode_id", b.EpisodeID), slog.String("adapter", emptyIfNull(b.Adapter))}
	if b.SeasonBindingID != nil {
		attrs = append(attrs, slog.Int64("season_binding_id", *b.SeasonBindingID))
	}
	attrs = append(attrs, f.LogAttrs...)
	attrs = append(attrs, slog.Int64("added", added), slog.Int("total", int(b.DanmakuCount)))
	s.logger.LogAttrs(ctx, slog.LevelInfo, "danmaku fetched", attrs...)
}

// linkAdapter 贴链接建的绑定的适配器。用弹幕文件建的绑定没有适配器，调用方要先按 kind 分支；
// 适配器没有注册时返回错误，调用方按服务器内部错误处理。
func (s *Service) linkAdapter(b bindingdb.Binding) (source.Adapter, error) {
	if b.Adapter == nil {
		return nil, fmt.Errorf("binding %d: %s binding has no adapter", b.ID, b.Kind)
	}
	adapter, err := s.sources.Get(*b.Adapter)
	if err != nil {
		return nil, fmt.Errorf("binding %d: %w", b.ID, err)
	}
	return adapter, nil
}
