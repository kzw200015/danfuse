package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// 追更的时间规则，写死在代码里，不加配置项。
const (
	followScanInterval  = time.Minute         // 后台扫描的间隔
	followCheckInterval = 24 * time.Hour      // 检查合集的周期，也是每个绑定自动重新拉取的最短间隔
	followRefetchWindow = 14 * 24 * time.Hour // 自动重新拉取的窗口：绑定建出后 14 天
)

// internalErrorMessage 补建遇到服务器内部错误时记在季绑定、条目上的原因，完整的错误进日志。
const internalErrorMessage = "服务器内部错误，详见日志"

var (
	// errSeasonBindingGone 补建期间季绑定被删除：这一轮随即结束，什么都不再写。
	errSeasonBindingGone = errors.New("season binding deleted")
	// errEpisodeGone 补建一个条目期间对应的集被删除：跳过这个条目。
	errEpisodeGone = errors.New("episode deleted")
)

// follow 追更的扫描：每分钟一次，按上次检查时间从早到晚、一次一个地补建到期的季绑定（ListDueSeasonBindings）。
// 正在补建的（手动触发或其他实例）跳过；列出之后才检查过的（拿到锁之后再确认一次）也跳过。
// 与同步不耦合：新集靠"这一季里有集晚于上次检查时间建出"在一分钟内被发现。
func (s *SeasonBindingService) follow(ctx context.Context) {
	ticker := time.NewTicker(followScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

func (s *SeasonBindingService) scan(ctx context.Context) {
	ids, err := s.store.ListDueSeasonBindings(ctx, dueParams(nil))
	if err != nil {
		if ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "list due season bindings failed", "error", err)
		}
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		unlock, ok, err := database.TryAdvisoryLockPair(ctx, s.pool, database.LockSeasonBackfill, id)
		if err != nil {
			if ctx.Err() == nil {
				s.logger.ErrorContext(ctx, "acquire backfill lock failed", "season_binding_id", id, "error", err)
			}
			continue
		}
		if !ok {
			continue
		}
		// 列出之后、拿到锁之前，它可能刚被手动补建或其他实例的扫描检查过
		switch due, err := s.store.ListDueSeasonBindings(ctx, dueParams(&id)); {
		case err != nil:
			if ctx.Err() == nil {
				s.logger.ErrorContext(ctx, "check season binding due failed", "season_binding_id", id, "error", err)
			}
		case len(due) > 0:
			s.backfill(ctx, id)
		}
		unlock()
	}
}

// dueParams 按现在的时间判定追更是否到期的参数；id 不为 nil 时只判定这一个季绑定。
func dueParams(id *int64) repository.ListDueSeasonBindingsParams {
	now := time.Now()
	return repository.ListDueSeasonBindingsParams{
		ID:                   id,
		CheckedBefore:        now.Add(-followCheckInterval),
		CreatedAfter:         now.Add(-followRefetchWindow),
		FetchedBefore:        now.Add(-followCheckInterval),
		CheckIntervalSeconds: int32(followCheckInterval / time.Second),
	}
}

// backfillRound 一轮补建的进度与结果。
type backfillRound struct {
	id    int64
	start time.Time // 这一轮的开始时间，结束时写为上次检查时间

	created, handled, failed, refetched int  // 建出的绑定、直接记为处理过的条目、失败的条目与重新拉取、重新拉取的绑定
	rateLimited                         bool // 因限流结束
	lastError                           *string
	dead                                bool // 合集已不存在
}

// backfill 补建一轮，调用方持有这个季绑定的锁：
//  1. 在事务之外列出合集：NotFound 标为失效、限流和其他上游错误记下原因，都结束这一轮；成功则恢复为正常，保存条目。
//  2. 按合集顺序逐个处理还没处理过、对得上、目录里有对应的集的条目（见 backfillItem）。
//  3. 追更开着时，自动重新拉取它建出的、建出不到 14 天、距上次拉取已满 24 小时的绑定（见 refetchRecent）。
//  4. 正常结束或因错误、限流结束时，把上次检查时间写为这一轮的开始时间：补建期间同步进来的集仍算"上次检查之后才有的"，
//     一分钟后会被再扫到。被 ctx 取消（关闭服务）时不写，重启后一分钟内接着做。
func (s *SeasonBindingService) backfill(ctx context.Context, id int64) {
	r := &backfillRound{id: id, start: time.Now()}
	err := s.runBackfill(ctx, r)
	switch {
	case ctx.Err() != nil:
		s.logger.Info("backfill interrupted", "season_binding_id", id)
		return
	case errors.Is(err, errSeasonBindingGone):
		s.logger.Info("season binding deleted during backfill", "season_binding_id", id)
		return
	case err != nil:
		s.logger.Error("backfill failed", "season_binding_id", id, "error", err)
		r.lastError = new(internalErrorMessage)
	}
	err = s.store.FinishSeasonBindingCheck(ctx, repository.FinishSeasonBindingCheckParams{
		ID: id, CheckedAt: r.start, Error: r.lastError, Dead: r.dead,
	})
	if err != nil {
		s.logger.Error("save backfill result failed", "season_binding_id", id, "error", err)
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "backfill finished",
		slog.Int64("season_binding_id", id),
		slog.Int("created", r.created),
		slog.Int("handled", r.handled),
		slog.Int("failed", r.failed),
		slog.Int("refetched", r.refetched),
		slog.Bool("rate_limited", r.rateLimited),
		slog.String("error", emptyIfNull(r.lastError)),
	)
}

// runBackfill 一轮补建的步骤 1～3。上游错误记在 r 里正常返回；季绑定被删除时返回 errSeasonBindingGone；
// 其余返回的错误是服务器内部错误。
func (s *SeasonBindingService) runBackfill(ctx context.Context, r *backfillRound) error {
	sb, err := s.getSeasonBinding(ctx, r.id)
	if err != nil {
		return err
	}
	adapter, err := s.sources.Get(sb.Adapter)
	if err != nil {
		return err
	}
	collector, err := collectorOf(adapter)
	if err != nil {
		return err
	}

	listCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	col, err := collector.ListCollection(listCtx, sb.Ref)
	cancel()
	if err != nil {
		srcErr, ok := errors.AsType[*source.Error](err)
		if !ok {
			return err
		}
		r.lastError, r.dead, r.rateLimited = &srcErr.Message, srcErr.Kind == source.NotFound, srcErr.Kind == source.RateLimited
		return nil
	}
	err = s.store.ExecTx(ctx, func(q repository.Querier) error {
		if _, err := q.RecordSeasonBindingListed(ctx, repository.RecordSeasonBindingListedParams{
			ID: r.id, Title: col.Title, Finished: col.Finished,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errSeasonBindingGone
			}
			return fmt.Errorf("record season binding %d listed: %w", r.id, err)
		}
		return saveItems(ctx, q, r.id, normalizeItems(col.Items))
	})
	if err != nil {
		return err
	}

	// 条目和处理过的记录都从库里读：两边的 ref 都是 jsonb 的输出格式，可以直接比较
	items, err := s.store.ListSeasonBindingItems(ctx, r.id)
	if err != nil {
		return fmt.Errorf("list items of season binding %d: %w", r.id, err)
	}
	handled, err := s.store.ListSeasonBindingHandled(ctx, r.id)
	if err != nil {
		return fmt.Errorf("list handled of season binding %d: %w", r.id, err)
	}
	done := make(map[string]bool, len(handled))
	for _, h := range handled {
		done[string(h.Ref)] = true
	}
	for _, it := range items {
		if done[string(it.Ref)] || it.Number == nil {
			continue
		}
		if stop, err := s.backfillItem(ctx, r, adapter, it); stop || err != nil {
			return err
		}
	}
	return s.refetchRecent(ctx, r)
}

// getSeasonBinding 补建时读季绑定，已被删除时返回 errSeasonBindingGone。
func (s *SeasonBindingService) getSeasonBinding(ctx context.Context, id int64) (repository.SeasonBinding, error) {
	sb, err := s.store.GetSeasonBinding(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return repository.SeasonBinding{}, errSeasonBindingGone
	}
	if err != nil {
		return repository.SeasonBinding{}, fmt.Errorf("get season binding %d: %w", id, err)
	}
	return sb, nil
}

// backfillItem 处理一个还没处理过、有序号的条目。处理之前重新读一次季绑定，补建进行中改了集号对应时从这个条目起用新的对应。
// 在起点之前、目录里没有对应的集时跳过；对应的集上已有同一个弹幕源的绑定时直接记为处理过，不拉取。
// 否则在事务之外拉取，再开写入事务：锁住季绑定（没有了就结束这一轮）、锁住集（没有了就跳过这个条目），
// 插入带来源季绑定的绑定（撞上唯一约束时视为已有同一个弹幕源，只记处理过），写入弹幕，记处理过，清掉条目的失败原因。
// 拉取失败时：限流记下原因、结束这一轮（stop 为 true）；其他错误只记在条目上，继续下一个。
func (s *SeasonBindingService) backfillItem(ctx context.Context, r *backfillRound, adapter source.Adapter, it repository.SeasonBindingItem) (stop bool, err error) {
	sb, err := s.getSeasonBinding(ctx, r.id)
	if err != nil {
		return true, err
	}
	target, ok := mappedEpisode(sb, *it.Number)
	if !ok {
		return false, nil
	}
	episodeID, err := s.store.GetEpisodeIDByNumber(ctx, repository.GetEpisodeIDByNumberParams{SeasonID: sb.SeasonID, Number: target})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // 等目录里出现这一集
	}
	if err != nil {
		return true, fmt.Errorf("get episode %d of season %d: %w", target, sb.SeasonID, err)
	}

	key := repository.BindingExistsParams{EpisodeID: episodeID, Adapter: sb.Adapter, Ref: it.Ref}
	switch bound, err := s.store.BindingExists(ctx, key); {
	case err != nil:
		return true, fmt.Errorf("check binding of episode %d: %w", episodeID, err)
	case bound:
		return false, s.saveBackfilled(ctx, r, sb, episodeID, it.Ref, nil)
	}

	fetched, err := fetch(ctx, adapter, it.Ref)
	if err != nil {
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		srcErr, ok := errors.AsType[*source.Error](err)
		if ok && srcErr.Kind == source.RateLimited {
			r.lastError, r.rateLimited = &srcErr.Message, true
			return true, nil
		}
		message := internalErrorMessage
		if ok {
			message = srcErr.Message
		}
		s.logger.Info("backfill item failed", "season_binding_id", r.id, "episode_id", episodeID, "error", err)
		r.failed++
		return false, s.setItemError(ctx, r.id, it.Ref, &message)
	}
	return false, s.saveBackfilled(ctx, r, sb, episodeID, it.Ref, &fetched)
}

// saveBackfilled 补建一个条目的写入事务。fetched 为 nil 时只记处理过（对应的集上已有同一个弹幕源的绑定）。
// 集在拉取期间被删除时跳过这个条目；季绑定被删除时返回 errSeasonBindingGone。
func (s *SeasonBindingService) saveBackfilled(ctx context.Context, r *backfillRound, sb repository.SeasonBinding, episodeID int64, ref []byte, fetched *source.Fetched) error {
	now := time.Now()
	var (
		binding repository.Binding
		added   int64
		created bool
	)
	err := s.store.ExecTx(ctx, func(q repository.Querier) error {
		if _, err := q.LockSeasonBindingShared(ctx, sb.ID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errSeasonBindingGone
			}
			return fmt.Errorf("lock season binding %d: %w", sb.ID, err)
		}
		if _, err := q.LockEpisode(ctx, episodeID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errEpisodeGone
			}
			return fmt.Errorf("lock episode %d: %w", episodeID, err)
		}
		if fetched != nil {
			id, err := q.InsertBackfilledBinding(ctx, repository.InsertBackfilledBindingParams{
				EpisodeID:       episodeID,
				Adapter:         sb.Adapter,
				Ref:             ref,
				Title:           fetched.Title,
				Duration:        int32(fetched.Duration),
				SeasonBindingID: sb.ID,
				CreatedAt:       now,
			})
			switch {
			case errors.Is(err, pgx.ErrNoRows): // 拉取期间有人手动绑定了同一个弹幕源
			case err != nil:
				return fmt.Errorf("insert binding of episode %d: %w", episodeID, err)
			default:
				if binding, added, err = saveFetched(ctx, q, id, *fetched, false, now); err != nil {
					return err
				}
				created = true
			}
		}
		if err := q.InsertSeasonBindingHandled(ctx, repository.InsertSeasonBindingHandledParams{
			SeasonBindingID: sb.ID, Ref: ref, EpisodeID: episodeID,
		}); err != nil {
			return fmt.Errorf("insert handled of season binding %d: %w", sb.ID, err)
		}
		return q.SetSeasonBindingItemError(ctx, repository.SetSeasonBindingItemErrorParams{SeasonBindingID: sb.ID, Ref: ref})
	})
	switch {
	case errors.Is(err, errEpisodeGone):
		return nil
	case err != nil:
		return err
	case created:
		r.created++
		s.bindings.logFetched(ctx, binding, *fetched, added)
	default:
		r.handled++
	}
	return nil
}

// setItemError 记下条目补建失败的原因和时间，下次补建再试。
func (s *SeasonBindingService) setItemError(ctx context.Context, id int64, ref []byte, message *string) error {
	err := s.store.SetSeasonBindingItemError(ctx, repository.SetSeasonBindingItemErrorParams{
		SeasonBindingID: id, Ref: ref, Error: message, ErrorAt: new(time.Now()),
	})
	if err != nil {
		return fmt.Errorf("set item error of season binding %d: %w", id, err)
	}
	return nil
}

// refetchRecent 追更开着时（不论这一轮由什么触发），按上次拉取时间从早到晚重新拉取这个季绑定建出的、建出不到 14 天、
// 距上次拉取已满 24 小时的绑定，复用 BindingService.Refetch 的只增不删模式：NotFound 照旧标为失效，限流结束这一轮。
// 是否满 24 小时在拉取每个绑定之前按当时的时间判断，前面的绑定拉取期间到期的也接着拉取。
func (s *SeasonBindingService) refetchRecent(ctx context.Context, r *backfillRound) error {
	candidates, err := s.store.ListRecentBackfilledBindings(ctx, repository.ListRecentBackfilledBindingsParams{
		SeasonBindingID: r.id, CreatedAfter: time.Now().Add(-followRefetchWindow),
	})
	if err != nil {
		return fmt.Errorf("list recent bindings of season binding %d: %w", r.id, err)
	}
	for _, c := range candidates {
		if c.LastFetchedAt != nil && c.LastFetchedAt.After(time.Now().Add(-followCheckInterval)) {
			return nil // 按上次拉取时间排序，后面的也没到 24 小时
		}
		sb, err := s.getSeasonBinding(ctx, r.id)
		if err != nil {
			return err
		}
		if !sb.Follow {
			return nil
		}
		_, _, err = s.bindings.Refetch(ctx, c.ID, false)
		switch srcErr, _ := errors.AsType[*source.Error](err); {
		case err == nil:
			r.refetched++
		case ctx.Err() != nil:
			return ctx.Err()
		case srcErr != nil && srcErr.Kind == source.RateLimited:
			r.lastError, r.rateLimited = &srcErr.Message, true
			return nil
		case errors.Is(err, errBindingNotFound), errors.Is(err, errBindingDeleted): // 用户刚删掉了这个绑定
		case srcErr != nil: // NotFound 已标为失效；其余上游错误下次再试
			r.failed++
		default:
			return err
		}
	}
	return nil
}
