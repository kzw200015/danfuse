package seasonbinding

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/infra/background"
	"github.com/kzw200015/danfuse/backend/internal/infra/database"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/dao"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// 合集的后台补建与追更：后台循环、租约、追更的扫描、一轮补建。

// errSeasonBindingGone 补建期间季绑定被删除：这一轮随即结束，什么都不再写。
var errSeasonBindingGone = errors.New("season binding deleted")

// 补建的触发方式，只用于日志，取值同同步（catalog 包里 sync_runs.trigger 的取值）。
const (
	triggerManual   = "manual"   // 创建、保存、立即补建、打开追更
	triggerSchedule = "schedule" // 追更的扫描
)

// internalErrorMessage 补建遇到服务器内部错误时存在季绑定、条目上给管理界面看的原因，完整的错误进日志。
const internalErrorMessage = "服务器内部错误，详见日志"

// Run 后台循环，阻塞到 ctx 取消；返回前等进行中的扫描和补建停下。只能调用一次。
// 每隔 follow.scan_interval 在后台开始一次追更的扫描（见 scan），启动时不立即扫描；手动触发拿到租约就在后台开始补建，
// 不同季绑定的补建互不等待（对平台的请求由适配器自己限速）。
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.follow.ScanInterval)
	defer ticker.Stop()
	s.loop.Run(ctx, ticker.C, func(ctx context.Context) {
		s.loop.Spawn(func() { s.scan(ctx) })
	})
}

// trigger 请 Run 的循环立即在后台补建一次。正在补建时返回 errBackfillRunning；
// Run 已经返回（服务正在关闭）时返回 background.ErrStopped（503）。
func (s *Service) trigger(ctx context.Context, id int64) error {
	return s.loop.Call(ctx, func(ctx context.Context) error { return s.start(ctx, id) })
}

// startBackfill 创建、保存、打开追更之后随即在后台补建一次：正在补建时不另起一轮；服务正在关闭时不补建，
// 追更开着的季绑定重启后由扫描接着做。都不是这次请求的错误，只记日志。
func (s *Service) startBackfill(ctx context.Context, id int64) {
	err := s.trigger(ctx, id)
	if err != nil && !errors.Is(err, errBackfillRunning) && !errors.Is(err, background.ErrStopped) && ctx.Err() == nil {
		s.logger.ErrorContext(ctx, "start backfill failed", "season_binding_id", id, "error", err)
	}
}

// start 拿到按季绑定的租约就在后台补建一轮，租约由补建的 goroutine 持有到结束；拿不到时返回 errBackfillRunning。
func (s *Service) start(ctx context.Context, id int64) error {
	lease, err := s.tryBackfillLease(ctx, id)
	if err != nil {
		return err
	}
	s.loop.Spawn(func() {
		defer lease.Release()
		s.backfill(lease.Context(), id, triggerManual, nil)
	})
	return nil
}

// tryBackfillLease 拿按季绑定的补建租约：补建与保存表单都要持有它；拿不到（正在补建）时返回 errBackfillRunning。
func (s *Service) tryBackfillLease(ctx context.Context, id int64) (*database.Lease, error) {
	lease, ok, err := database.TryLease(ctx, s.pool, s.logger, database.LeaseSeasonBackfill(id))
	if err != nil {
		return nil, fmt.Errorf("acquire backfill lease of season binding %d: %w", id, err)
	}
	if !ok {
		return nil, errBackfillRunning
	}
	return lease, nil
}

// scan 追更的扫描，Run 每隔 follow.scan_interval 在后台开始一次：按上次检查时间从早到晚、一次一个地补建到期的季绑定（ListDueSeasonBindings）。
// 上一次扫描还没做完时两次扫描同时进行：正在补建的（另一次扫描、手动触发或其他实例）跳过，
// 列出之后才检查过的（拿到租约之后再确认一次）也跳过，同一个季绑定不会重复补建。
// 与同步不耦合：新集靠"这一季里有集晚于上次检查时间建出"在一个扫描间隔内被发现。
// 有到期的季绑定时，扫描做完记一条 "follow scan finished"：到期几个、补建了几个、跳过了几个；没有到期的不记，免得每次扫描一条。
func (s *Service) scan(ctx context.Context) {
	start := time.Now()
	due, err := s.q.ListDueSeasonBindings(ctx, s.dueParams(nil))
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
func (s *Service) scanOne(ctx context.Context, id int64) bool {
	lease, err := s.tryBackfillLease(ctx, id)
	if err != nil {
		if !errors.Is(err, errBackfillRunning) && ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "acquire backfill lease failed", "season_binding_id", id, "error", err)
		}
		return false
	}
	defer lease.Release()
	ctx = lease.Context()

	// 列出之后、拿到租约之前，它可能刚被手动补建或其他实例的扫描检查过
	due, err := s.q.ListDueSeasonBindings(ctx, s.dueParams(&id))
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
func dueReasons(d dao.ListDueSeasonBindingsRow) []string {
	var reasons []string
	for _, r := range []struct {
		name string
		ok   bool
	}{
		{"never_checked", d.NeverChecked},
		{"interval_due", d.IntervalDue},
		{"new_episodes", d.NewEpisodes},
	} {
		if r.ok {
			reasons = append(reasons, r.name)
		}
	}
	return reasons
}

// dueParams 按现在的时间和追更的检查周期（follow.check_interval）判定追更是否到期的参数；
// id 不为 nil 时只判定这一个季绑定。
func (s *Service) dueParams(id *int64) dao.ListDueSeasonBindingsParams {
	return dao.ListDueSeasonBindingsParams{ID: id, DueBefore: time.Now().Add(-s.follow.CheckInterval)}
}

// backfillRound 一轮补建的进度与结果。
type backfillRound struct {
	id       int64
	start    time.Time // 这一轮的开始时间，结束时写为上次检查时间
	seasonID int64     // 读到季绑定之后才有

	items        int   // 列出的合集条目数
	created      int   // 新建的绑定
	replaced     int   // 替换的绑定
	alreadyBound int   // 对到的集上已有同一个弹幕源的绑定而跳过的条目（含写入时才撞上的）
	unmatched    int   // 对不上的条目
	beforeStart  int   // 序号在集号对应的起点之前的条目
	noEpisode    int   // 目录里没有对到的集的条目
	failed       int   // 拉取失败的条目
	danmakuAdded int64 // 建出的绑定新增的弹幕条数

	rateLimited bool // 因限流结束
	lastError   *string
	dead        bool // 合集已不存在
}

// backfill 补建一轮，调用方持有这个季绑定的租约，ctx 是租约的 ctx：
//  1. 在事务之外列出合集：NotFound 标为失效、限流和其他上游错误记下原因，都结束这一轮，不动绑定和条目表。
//  2. 在一个事务里保存列出的结果、派生（见 runBackfill）。
//  3. 逐个处理"新建"和"替换"的条目（见 backfillItem）。补建从不删除没有条目的槽位。
//  4. 正常结束或因错误、限流结束时，把上次检查时间写为这一轮的开始时间：补建期间同步进来的集仍算"上次检查之后才有的"，
//     下一次扫描会再扫到。被 ctx 取消（关闭服务、租约丢失）时不写，下一次扫描接着做。
//
// trigger、due 只用于日志：due 是追更的扫描触发时到期的原因（dueReasons），手动触发时为 nil。
// 结束时记一条 "backfill finished"：为什么到期、条目各自停在哪一步、建出了多少弹幕；记下了错误时为 warn 级别。
func (s *Service) backfill(ctx context.Context, id int64, trigger string, due []string) {
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
	err = s.q.FinishSeasonBindingCheck(ctx, dao.FinishSeasonBindingCheckParams{
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
		slog.Int("replaced", r.replaced),
		slog.Int("already_bound", r.alreadyBound),
		slog.Int("unmatched", r.unmatched),
		slog.Int("before_start", r.beforeStart),
		slog.Int("no_episode", r.noEpisode),
		slog.Int("failed", r.failed),
		slog.Int64("danmaku_added", r.danmakuAdded),
		slog.Bool("rate_limited", r.rateLimited),
		slog.Bool("dead", r.dead),
		slog.String("error", emptyIfNull(r.lastError)),
	)
}

// runBackfill 一轮补建的步骤 1 到 3。上游错误记在 r 里正常返回；季绑定被删除时返回 errSeasonBindingGone；
// 其余返回的错误是服务器内部错误。只会遇到合集的季绑定：文件夹的季绑定追更恒为关，立即补建按种类分派。
func (s *Service) runBackfill(ctx context.Context, r *backfillRound) error {
	sb, err := s.getSeasonBinding(ctx, r.id, errSeasonBindingGone)
	if err != nil {
		return err
	}
	r.seasonID = sb.SeasonID
	adapter, err := s.sources.Get(*sb.Adapter)
	if err != nil {
		return err
	}

	listCtx, cancel := context.WithTimeout(ctx, source.FetchTimeout)
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

	var plan Result
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// 表单在锁住季绑定的那一句里读：保存表单的事务要么已经提交、这里读到新的表单，要么等这个事务提交
		form, err := saveListed(ctx, q, r.id, col, errSeasonBindingGone)
		if err != nil {
			return err
		}
		rule, err := formRule(form)
		if err != nil {
			return err
		}
		episodes, err := episodeSlots(ctx, q, form.SeasonID, &form.ID, *form.Adapter)
		if err != nil {
			return err
		}
		plan = Derive(collectionEntries(source.NumberItems(col, rule)), formMapping(form), episodes)
		return nil
	})
	if err != nil {
		return err
	}

	r.items = len(plan.Items)
	for _, it := range plan.Items {
		switch it.Status {
		case StatusAlreadyBound:
			r.alreadyBound++
		case StatusUnmatched:
			r.unmatched++
		case StatusBeforeStart:
			r.beforeStart++
		case StatusNoEpisode:
			r.noEpisode++
		case StatusCreate, StatusReplace:
			if stop, err := s.backfillItem(ctx, r, sb, adapter, it); stop || err != nil {
				return err
			}
		}
	}
	return nil
}

// backfillItem 新建或替换一个槽位：在事务之外拉取，再写入（见 saveSlot）。
// 拉取失败时：限流记下原因、结束这一轮（stop 为 true）；其他错误记在条目上，继续下一个。
func (s *Service) backfillItem(ctx context.Context, r *backfillRound, sb dao.SeasonBinding, adapter source.Adapter, it Item) (stop bool, err error) {
	ref := source.Ref(it.Entry.Source) // 规范化的 ref 是合法的 JSON，原样交给适配器
	fetchCtx, cancel := context.WithTimeout(ctx, source.FetchTimeout)
	fetched, err := adapter.Fetch(fetchCtx, ref)
	cancel()
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
		s.logger.Info("backfill item failed", "season_binding_id", r.id, "episode_id", it.EpisodeID, "error", err)
		r.failed++
		return false, s.setItemError(ctx, r.id, ref, &message)
	}
	return false, s.saveSlot(ctx, r, sb, it, ref, fetched)
}

// saveSlot 补建一个槽位的写入事务：锁住季、季绑定（没有了就结束这一轮，返回 errSeasonBindingGone），
// 替换时先删掉槽位里原来的绑定，再由 binding.Service.CreateLinkSlotInTx 锁住集、建出绑定、写入弹幕，最后清掉条目的失败原因。
// 拉取期间集被删除（ErrEpisodeGone）时跳过这个条目；有人在那一集绑定了同一个弹幕源（ErrSourceBound）时整个事务回滚，
// 原来的绑定保留，计入 alreadyBound，再另外清掉条目的失败原因。
func (s *Service) saveSlot(ctx context.Context, r *backfillRound, sb dao.SeasonBinding, it Item, ref source.Ref, fetched source.Fetched) error {
	now := time.Now()
	var saved binding.Saved
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		// 先锁季：删季的级联先锁集、后锁季绑定，与这里相反；两边都先锁季，在第一步就排队，不会死锁
		if err := lockSeason(ctx, q, sb.SeasonID, errSeasonBindingGone); err != nil { // 季删除时季绑定随之删除
			return err
		}
		if _, err := q.LockSeasonBindingShared(ctx, sb.ID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errSeasonBindingGone
			}
			return fmt.Errorf("lock season binding %d: %w", sb.ID, err)
		}
		if it.Status == StatusReplace {
			if err := s.bindings.DeleteSlotsInTx(ctx, tx, sb.ID, []int64{it.EpisodeID}); err != nil {
				return err
			}
		}
		var err error
		saved, err = s.bindings.CreateLinkSlotInTx(ctx, tx, binding.LinkSlot{
			SeasonBindingID: sb.ID, EpisodeID: it.EpisodeID, Adapter: *sb.Adapter, Ref: ref, Fetched: fetched, CreatedAt: now,
		})
		if err != nil {
			return err
		}
		return q.SetSeasonBindingItemError(ctx, dao.SetSeasonBindingItemErrorParams{SeasonBindingID: sb.ID, Ref: ref})
	})
	switch {
	case errors.Is(err, binding.ErrEpisodeGone):
		return nil
	case errors.Is(err, binding.ErrSourceBound):
		r.alreadyBound++
		return s.setItemError(ctx, r.id, ref, nil) // 跳过不算失败，清掉上次失败的原因
	case err != nil:
		return err
	}
	if it.Status == StatusReplace {
		r.replaced++
	} else {
		r.created++
	}
	r.danmakuAdded += saved.Added
	s.bindings.LogFetched(ctx, saved)
	return nil
}

// setItemError 记下条目补建失败的原因和时间，下次补建再试；message 为 nil 时清掉两者。
func (s *Service) setItemError(ctx context.Context, id int64, ref []byte, message *string) error {
	p := dao.SetSeasonBindingItemErrorParams{SeasonBindingID: id, Ref: ref, Error: message}
	if message != nil {
		p.ErrorAt = new(time.Now())
	}
	err := s.q.SetSeasonBindingItemError(ctx, p)
	if err != nil {
		return fmt.Errorf("set item error of season binding %d: %w", id, err)
	}
	return nil
}
