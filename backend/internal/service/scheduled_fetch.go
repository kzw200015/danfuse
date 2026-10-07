package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// scheduledFetchScanInterval 定时拉取的扫描间隔：到期的绑定最迟过这么久开始拉取。
// 不设配置项：与 scheduled_fetch.interval 相比只是一点延迟，没有调它的理由。
const scheduledFetchScanInterval = time.Minute

// ScheduledFetchService 定时拉取：能重新拉取的绑定，建出后 scheduled_fetch.window 之内，
// 距上次尝试拉取满 scheduled_fetch.interval 时自动重新拉取，不论绑定是手动建的还是补建出的，与追更无关。
//
// App.Run 运行 Run(ctx)：每隔 scheduledFetchScanInterval 在 Run 的 goroutine 里扫描一次，上一轮没做完时这一次跳过；
// ctx 取消时进行中的一轮停下，Run 随即返回。窗口为 0 时关闭，Run 只等 ctx 取消。
//
// 多实例时同一时间也只跑一轮：每轮持有全局的租约（database.LeaseScheduledFetch），拿不到就跳过这次扫描；
// 一轮里一个接一个地拉取，对平台的请求不并发。写入靠 BindingService.refetch 的行锁，与手动重新拉取同时进行也不会出错。
type ScheduledFetchService struct {
	store    *repository.Store
	pool     *pgxpool.Pool   // 拿定时拉取的租约
	bindings *BindingService // 复用它的 refetch
	rule     config.ScheduledFetch
	logger   *slog.Logger
}

func NewScheduledFetchService(store *repository.Store, pool *pgxpool.Pool, bindings *BindingService, rule config.ScheduledFetch, logger *slog.Logger) *ScheduledFetchService {
	return &ScheduledFetchService{
		store:    store,
		pool:     pool,
		bindings: bindings,
		rule:     rule,
		logger:   logger,
	}
}

// Run 后台循环，阻塞到 ctx 取消；返回前进行中的一轮已经停下。启动时不立即扫描。
func (s *ScheduledFetchService) Run(ctx context.Context) {
	if s.rule.Window == 0 {
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(scheduledFetchScanInterval)
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

// scheduledFetchRound 一轮定时拉取的结果，只用于日志。
type scheduledFetchRound struct {
	fetched      int   // 拉取成功的
	failed       int   // 拉取失败的（弹幕源不存在而标为失效的另计）
	dead         int   // 弹幕源不存在、标为失效的
	danmakuAdded int64 // 新增的弹幕条数

	rateLimited bool // 因限流结束
}

// scan 一轮定时拉取：列出到期的绑定（ListDueScheduledFetches），有到期的才拿租约（其他实例正在拉取时拿不到，跳过这次扫描），
// 按上次尝试拉取的时间从早到晚一个接一个地重新拉取（只增不删，见 fetchOne）；限流时结束这一轮，下一次扫描接着做。
// 列出时不持有租约：其他实例可能刚拉取过其中一些，fetchOne 拉取每个绑定之前会再确认一次仍然到期。
// 有到期的绑定时，结束时记一条 "scheduled fetch finished"；没有的不记，免得每次扫描一条。
func (s *ScheduledFetchService) scan(ctx context.Context) {
	start := time.Now()
	due, err := s.store.ListDueScheduledFetches(ctx, s.dueParams(nil))
	if err != nil {
		if ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "list due scheduled fetches failed", "error", err)
		}
		return
	}
	if len(due) == 0 {
		return
	}
	lease, ok, err := database.TryLease(ctx, s.pool, s.logger, database.LeaseScheduledFetch)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "acquire scheduled fetch lease failed", "error", err)
		}
		return
	}
	if !ok {
		return
	}
	defer lease.Release()
	ctx = lease.Context()

	r := &scheduledFetchRound{}
	for _, id := range due {
		if err = s.fetchOne(ctx, r, id); err != nil || r.rateLimited {
			break
		}
	}
	if ctx.Err() != nil {
		s.logger.Info("scheduled fetch interrupted", "cause", context.Cause(ctx))
		return
	}
	if err != nil {
		s.logger.Error("scheduled fetch failed", "error", err)
	}
	level := slog.LevelInfo
	if err != nil || r.rateLimited {
		level = slog.LevelWarn
	}
	s.logger.LogAttrs(ctx, level, "scheduled fetch finished",
		slog.Duration("duration", time.Since(start)),
		slog.Int("due", len(due)),
		slog.Int("fetched", r.fetched),
		slog.Int("failed", r.failed),
		slog.Int("dead", r.dead),
		slog.Int64("danmaku_added", r.danmakuAdded),
		slog.Bool("rate_limited", r.rateLimited),
	)
}

// dueParams 按现在的时间和定时拉取的时间规则判定是否到期的参数；id 不为 nil 时只判定这一个绑定。
func (s *ScheduledFetchService) dueParams(id *int64) repository.ListDueScheduledFetchesParams {
	now := time.Now()
	return repository.ListDueScheduledFetchesParams{
		ID:           id,
		CreatedAfter: now.Add(-s.rule.Window),
		DueBefore:    now.Add(-s.rule.Interval),
	}
}

// fetchOne 重新拉取一个到期的绑定。拉取之前用同一条查询按当时的时间再确认一次仍然到期：列出之后它可能刚被手动重新拉取过，
// 或者已被删除，都跳过。复用 BindingService.refetch：失败时记下尝试拉取的时间，弹幕源不存在时标为失效；
// 限流记在 r 上，由调用方结束这一轮。返回的错误是服务器内部错误（或 ctx 的错误），结束这一轮。
func (s *ScheduledFetchService) fetchOne(ctx context.Context, r *scheduledFetchRound, id int64) error {
	due, err := s.store.ListDueScheduledFetches(ctx, s.dueParams(&id))
	if err != nil {
		return fmt.Errorf("check binding %d due: %w", id, err)
	}
	if len(due) == 0 {
		return nil
	}

	_, added, err := s.bindings.refetch(ctx, id, false)
	switch srcErr, _ := errors.AsType[*source.Error](err); {
	case err == nil:
		r.fetched++
		r.danmakuAdded += added
	case ctx.Err() != nil:
		return ctx.Err()
	case srcErr != nil && srcErr.Kind == source.RateLimited:
		r.rateLimited = true
	case srcErr != nil && srcErr.Kind == source.NotFound: // 已标为失效
		r.dead++
	case srcErr != nil: // 其余上游错误满一个间隔再试
		r.failed++
	case errors.Is(err, errBindingNotFound), errors.Is(err, errBindingDeleted): // 用户刚删掉了这个绑定
	default:
		return err
	}
	return nil
}
