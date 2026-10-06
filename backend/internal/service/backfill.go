package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	// errSeasonBindingGone 补建期间季绑定被删除：这一轮随即结束，什么都不再写。
	errSeasonBindingGone = errors.New("season binding deleted")
	// errEpisodeGone 补建一个条目期间对应的集被删除：跳过这个条目。
	errEpisodeGone = errors.New("episode deleted")
)

// scan 追更的扫描，Run 每隔 follow.scan_interval 在后台开始一次：按上次检查时间从早到晚、一次一个地补建到期的季绑定（ListDueSeasonBindings）。
// 上一次扫描还没做完时两次扫描同时进行：正在补建的（另一次扫描、手动触发或其他实例）跳过，
// 列出之后才检查过的（拿到租约之后再确认一次）也跳过，同一个季绑定不会重复补建。
// 与同步不耦合：新集靠"这一季里有集晚于上次检查时间建出"在一个扫描间隔内被发现。
// 有到期的季绑定时，扫描做完记一条 "follow scan finished"：到期几个、补建了几个、跳过了几个；没有到期的不记，免得每次扫描一条。
func (s *SeasonBindingService) scan(ctx context.Context) {
	start := time.Now()
	due, err := s.store.ListDueSeasonBindings(ctx, s.dueParams(nil))
	if err != nil {
		if ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "list due season bindings failed", "error", err)
		}
		return
	}
	if len(due) == 0 {
		return
	}
	backfilled := 0
	for _, d := range due {
		if ctx.Err() != nil {
			return
		}
		if s.scanOne(ctx, d.ID) {
			backfilled++
		}
	}
	s.logger.InfoContext(ctx, "follow scan finished",
		"due", len(due), "backfilled", backfilled, "skipped", len(due)-backfilled, "duration", time.Since(start))
}

// scanOne 拿到季绑定的租约、确认仍然到期后补建一轮，补建完随即释放；正在补建时跳过。补建了返回 true。
func (s *SeasonBindingService) scanOne(ctx context.Context, id int64) bool {
	lease, ok, err := database.TryLease(ctx, s.pool, s.logger, database.LeaseSeasonBackfill(id))
	if err != nil {
		if ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "acquire backfill lease failed", "season_binding_id", id, "error", err)
		}
		return false
	}
	if !ok {
		return false
	}
	defer lease.Release()
	ctx = lease.Context()

	// 列出之后、拿到租约之前，它可能刚被手动补建或其他实例的扫描检查过
	due, err := s.store.ListDueSeasonBindings(ctx, s.dueParams(&id))
	if err != nil {
		if ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "check season binding due failed", "season_binding_id", id, "error", err)
		}
		return false
	}
	if len(due) == 0 {
		return false
	}
	s.backfill(ctx, id, triggerSchedule, dueReasons(due[0]))
	return true
}

// dueReasons 追更到期的原因，按 ListDueSeasonBindings 的列名，只用于日志。
func dueReasons(d repository.ListDueSeasonBindingsRow) []string {
	var reasons []string
	for _, r := range []struct {
		name string
		ok   bool
	}{
		{"never_checked", d.NeverChecked},
		{"interval_due", d.IntervalDue},
		{"new_episodes", d.NewEpisodes},
		{"refetch_due", d.RefetchDue},
	} {
		if r.ok {
			reasons = append(reasons, r.name)
		}
	}
	return reasons
}

// dueParams 按现在的时间和追更的时间规则（follow.check_interval、follow.refetch_window）判定追更是否到期的参数；
// id 不为 nil 时只判定这一个季绑定。
func (s *SeasonBindingService) dueParams(id *int64) repository.ListDueSeasonBindingsParams {
	now := time.Now()
	return repository.ListDueSeasonBindingsParams{
		ID:                   id,
		DueBefore:            now.Add(-s.follow.CheckInterval),
		CreatedAfter:         now.Add(-s.follow.RefetchWindow),
		CheckIntervalSeconds: s.follow.CheckInterval.Seconds(),
	}
}

// backfillRound 一轮补建的进度与结果。
type backfillRound struct {
	id       int64
	start    time.Time // 这一轮的开始时间，结束时写为上次检查时间
	seasonID int64     // 读到季绑定之后才有

	items          int   // 列出的合集条目数
	created        int   // 建出的绑定
	alreadyBound   int   // 对应的集上已有同一个弹幕源的绑定、只记为处理过的条目
	unmatched      int   // 还没处理过、对不上（没有序号）的条目
	beforeStart    int   // 还没处理过、序号在集号对应的起点之前的条目
	waitingEpisode int   // 还没处理过、目录里还没有对应的集的条目
	failed         int   // 失败的条目与重新拉取
	refetched      int   // 重新拉取的绑定
	danmakuAdded   int64 // 建出与重新拉取的绑定新增的弹幕条数

	rateLimited bool // 因限流结束
	lastError   *string
	dead        bool // 合集已不存在
}

// backfill 补建一轮，调用方持有这个季绑定的租约，ctx 是租约的 ctx：
//  1. 在事务之外列出合集：NotFound 标为失效、限流和其他上游错误记下原因，都结束这一轮；成功则恢复为正常，保存条目。
//  2. 按合集顺序逐个处理还没处理过、对得上、目录里有对应的集的条目（见 backfillItem）。
//  3. 追更开着时，自动重新拉取它建出的、在重新拉取的窗口内、距上次拉取已满一个检查周期的绑定（见 refetchRecent）。
//  4. 正常结束或因错误、限流结束时，把上次检查时间写为这一轮的开始时间：补建期间同步进来的集仍算"上次检查之后才有的"，
//     下一次扫描会再扫到。被 ctx 取消（关闭服务、租约丢失）时不写，下一次扫描接着做。
//
// trigger、due 只用于日志：trigger 取值同同步（triggerSchedule 为追更的扫描，triggerManual 为创建、立即补建、改集号对应、打开追更），
// due 是追更的扫描触发时到期的原因（dueReasons），手动触发时为 nil。
// 结束时记一条 "backfill finished"，用来评估追更：为什么到期、条目各自停在哪一步、建出与重新拉取了多少弹幕；
// 这一轮记下了错误（上游错误、限流、合集已不存在、服务器内部错误）时为 warn 级别。
func (s *SeasonBindingService) backfill(ctx context.Context, id int64, trigger string, due []string) {
	r := &backfillRound{id: id, start: time.Now()}
	err := s.runBackfill(ctx, r)
	switch {
	case ctx.Err() != nil:
		s.logger.Info("backfill interrupted", "season_binding_id", id, "trigger", trigger, "cause", context.Cause(ctx))
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
	level := slog.LevelInfo
	if r.lastError != nil {
		level = slog.LevelWarn
	}
	s.logger.LogAttrs(ctx, level, "backfill finished",
		slog.Int64("season_binding_id", id),
		slog.Int64("season_id", r.seasonID),
		slog.String("trigger", trigger),
		slog.String("due", strings.Join(due, ",")),
		slog.Duration("duration", time.Since(r.start)),
		slog.Int("items", r.items),
		slog.Int("created", r.created),
		slog.Int("already_bound", r.alreadyBound),
		slog.Int("unmatched", r.unmatched),
		slog.Int("before_start", r.beforeStart),
		slog.Int("waiting_episode", r.waitingEpisode),
		slog.Int("failed", r.failed),
		slog.Int("refetched", r.refetched),
		slog.Int64("danmaku_added", r.danmakuAdded),
		slog.Bool("rate_limited", r.rateLimited),
		slog.Bool("dead", r.dead),
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
	r.seasonID = sb.SeasonID
	adapter, err := s.sources.Get(sb.Adapter)
	if err != nil {
		return err
	}

	listCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	col, err := adapter.ListCollection(listCtx, sb.Ref)
	cancel()
	if err != nil {
		srcErr, ok := errors.AsType[*source.Error](err)
		if !ok {
			return err
		}
		r.lastError, r.dead, r.rateLimited = &srcErr.Message, srcErr.Kind == source.NotFound, srcErr.Kind == source.RateLimited
		return nil
	}
	err = s.store.ExecTx(ctx, func(q *repository.Queries) error {
		// 集号规则在锁住季绑定的这一句里读：改规则的事务要么已经提交、这里读到新规则，要么等这个事务提交再按新规则重认
		patterns, err := q.RecordSeasonBindingListed(ctx, repository.RecordSeasonBindingListedParams{
			ID: r.id, Title: col.Title, Finished: col.Finished, NumberedByRule: col.NumberedByRule,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errSeasonBindingGone
			}
			return fmt.Errorf("record season binding %d listed: %w", r.id, err)
		}
		rule, err := source.ParseEpisodeRule(patterns)
		if err != nil {
			return fmt.Errorf("parse episode patterns of season binding %d: %w", r.id, err)
		}
		return saveItems(ctx, q, r.id, source.NumberItems(col, rule))
	})
	if err != nil {
		return err
	}

	// 条目和处理过的记录都从库里读：两边的 ref 都是 jsonb 的输出格式，可以直接比较
	items, err := s.store.ListSeasonBindingItems(ctx, r.id)
	if err != nil {
		return fmt.Errorf("list items of season binding %d: %w", r.id, err)
	}
	r.items = len(items)
	handled, err := s.store.ListSeasonBindingHandled(ctx, r.id)
	if err != nil {
		return fmt.Errorf("list handled of season binding %d: %w", r.id, err)
	}
	done := make(map[string]bool, len(handled))
	for _, h := range handled {
		done[string(h.Ref)] = true
	}
	for _, it := range items {
		if done[string(it.Ref)] {
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

// backfillItem 处理一个还没处理过的条目。处理之前重新读一次季绑定和条目的序号，补建进行中改了集号对应、集号规则时
// 从这个条目起用新的对应和序号。对不上（没有序号）、条目已经不在、在起点之前、目录里没有对应的集时跳过；对应的集上已有同一个弹幕源的绑定时直接记为处理过，不拉取。
// 否则在事务之外拉取，再开写入事务：锁住季绑定（没有了就结束这一轮）、锁住集（没有了就跳过这个条目），
// 插入带来源季绑定的绑定（撞上唯一约束时视为已有同一个弹幕源，只记处理过），写入弹幕，记处理过，清掉条目的失败原因。
// 拉取失败时：限流记下原因、结束这一轮（stop 为 true）；其他错误只记在条目上，继续下一个。
func (s *SeasonBindingService) backfillItem(ctx context.Context, r *backfillRound, adapter source.Adapter, it repository.SeasonBindingItem) (stop bool, err error) {
	sb, err := s.getSeasonBinding(ctx, r.id)
	if err != nil {
		return true, err
	}
	number, err := s.store.GetSeasonBindingItemNumber(ctx, repository.GetSeasonBindingItemNumberParams{SeasonBindingID: r.id, Ref: it.Ref})
	switch {
	case errors.Is(err, pgx.ErrNoRows): // 季绑定刚被删除，条目随之删除；下一个条目读季绑定时结束这一轮
		return false, nil
	case err != nil:
		return true, fmt.Errorf("get item number of season binding %d: %w", r.id, err)
	case number == nil:
		r.unmatched++
		return false, nil
	}
	target, ok := mappedEpisode(sb, *number)
	if !ok {
		r.beforeStart++
		return false, nil
	}
	episodeID, err := s.store.GetEpisodeIDByNumber(ctx, repository.GetEpisodeIDByNumberParams{SeasonID: sb.SeasonID, Number: target})
	if errors.Is(err, pgx.ErrNoRows) {
		r.waitingEpisode++
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

// saveBackfilled 补建一个条目的写入事务。fetched 为 nil 时只记处理过（对应的集上已有同一个弹幕源的绑定）；
// 只记了处理过、没有建出绑定的（包括拉取期间有人手动绑定了同一个弹幕源）计入 alreadyBound。
// 集在拉取期间被删除时跳过这个条目；季绑定被删除时返回 errSeasonBindingGone。
func (s *SeasonBindingService) saveBackfilled(ctx context.Context, r *backfillRound, sb repository.SeasonBinding, episodeID int64, ref []byte, fetched *source.Fetched) error {
	now := time.Now()
	var (
		binding repository.Binding
		added   int64
		created bool
	)
	err := s.store.ExecTx(ctx, func(q *repository.Queries) error {
		// 先锁季：删季的级联先锁集、后锁季绑定，补建若先锁季绑定、后锁集就会与它死锁；先锁住季，删季在第一步就排队
		if _, err := q.LockSeason(ctx, sb.SeasonID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errSeasonBindingGone // 季删除时季绑定随之删除
			}
			return fmt.Errorf("lock season %d: %w", sb.SeasonID, err)
		}
		if _, err := q.LockSeasonBindingShared(ctx, sb.ID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errSeasonBindingGone
			}
			return fmt.Errorf("lock season binding %d: %w", sb.ID, err)
		}
		if err := lockEpisode(ctx, q, episodeID, errEpisodeGone); err != nil {
			return err
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
		r.danmakuAdded += added
		s.bindings.logFetched(ctx, binding, *fetched, added)
	default:
		r.alreadyBound++
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

// refetchRecent 追更开着时（不论这一轮由什么触发），按上次拉取时间从早到晚重新拉取这个季绑定建出的、
// 建出不到 follow.refetch_window、距上次拉取已满 follow.check_interval 的绑定，
// 复用 BindingService.refetch 的只增不删模式：NotFound 照旧标为失效，限流结束这一轮。
// 是否满一个检查周期在拉取每个绑定之前按当时的时间判断，前面的绑定拉取期间到期的也接着拉取。
func (s *SeasonBindingService) refetchRecent(ctx context.Context, r *backfillRound) error {
	candidates, err := s.store.ListRecentBackfilledBindings(ctx, repository.ListRecentBackfilledBindingsParams{
		SeasonBindingID: r.id, CreatedAfter: time.Now().Add(-s.follow.RefetchWindow),
	})
	if err != nil {
		return fmt.Errorf("list recent bindings of season binding %d: %w", r.id, err)
	}
	for _, c := range candidates {
		if c.LastFetchedAt != nil && c.LastFetchedAt.After(time.Now().Add(-s.follow.CheckInterval)) {
			return nil // 按上次拉取时间排序，后面的也没满一个检查周期
		}
		sb, err := s.getSeasonBinding(ctx, r.id)
		if err != nil {
			return err
		}
		if !sb.Follow {
			return nil
		}
		_, added, err := s.bindings.refetch(ctx, c.ID, false)
		switch srcErr, _ := errors.AsType[*source.Error](err); {
		case err == nil:
			r.refetched++
			r.danmakuAdded += added
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
