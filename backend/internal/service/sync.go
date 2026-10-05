package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/repository"
)

const (
	maxSyncRuns     = 20  // 同步记录只保留最近 20 次
	maxSyncWarnings = 200 // 每次同步最多保存 200 条警告，另记总数

	// saveResultTimeout 写入最终状态的超时。最终状态在 ctx 取消后也要写，数据库不可达时不能让关闭永远挂住。
	saveResultTimeout = 5 * time.Second
)

// 同步的触发方式与状态，与 sync_runs 的 trigger、status 列的取值一致。
const (
	triggerManual   = "manual"
	triggerSchedule = "schedule"

	statusRunning     = "running"
	statusSucceeded   = "succeeded"
	statusFailed      = "failed"
	statusInterrupted = "interrupted"
)

var (
	errSyncRunning     = errcode.ErrConflict.WithMessage("同步正在进行")
	errNoCatalogSource = errcode.ErrConflict.WithMessage("未配置目录源")
	errSyncRunNotFound = errcode.ErrNotFound.WithMessage("同步记录不存在")
	errShuttingDown    = errcode.ErrServiceUnavailable.WithMessage("服务正在关闭")
)

// SyncService 同步：触发、定时、互斥、同步核心（按剧写入目录）、同步记录。
//
// 生命周期：App.Run 运行 Run(ctx)；手动触发经 Trigger 交给 Run 的循环。每次同步在独立的 goroutine 里执行，
// 用的是 Run 的 ctx（应用级），不是 HTTP 请求的 ctx。ctx 取消时这次同步记为 interrupted，Run 等它写完记录再返回，
// 之后 wire 的 cleanup 才关闭连接池。
//
// 互斥靠同步的租约（database.LeaseSync），多实例同样成立：每次同步持有租约直到结束，用租约的 ctx 执行，
// 租约丢失时这次同步同样记为 interrupted。
type SyncService struct {
	store    repository.Store
	pool     *pgxpool.Pool  // 拿同步的租约
	source   catalog.Source // nil 表示未配置目录源
	interval time.Duration
	logger   *slog.Logger

	triggers chan chan triggerResult // 手动触发：Run 的循环收到后开始同步，把结果送回
	stopped  chan struct{}           // Run 返回时关闭，之后的手动触发不再等它
	wg       sync.WaitGroup          // 进行中的同步
}

type triggerResult struct {
	runID int64
	err   error
}

func NewSyncService(store repository.Store, pool *pgxpool.Pool, src catalog.Source, cfg config.Sync, logger *slog.Logger) *SyncService {
	return &SyncService{
		store:    store,
		pool:     pool,
		source:   src,
		interval: cfg.Interval,
		logger:   logger,
		triggers: make(chan chan triggerResult),
		stopped:  make(chan struct{}),
	}
}

// Run 后台循环，阻塞到 ctx 取消；返回前等进行中的同步写完最终状态。只能调用一次。
// 启动时先清理残留的 running；配置了目录源且 interval > 0 时每隔一个间隔触发一次，启动时不立即同步。
func (s *SyncService) Run(ctx context.Context) {
	defer close(s.stopped)
	s.cleanupStale(ctx)

	var tick <-chan time.Time
	if s.source != nil && s.interval > 0 {
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		tick = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return
		case <-tick:
			switch _, err := s.start(ctx, triggerSchedule); {
			case errors.Is(err, errSyncRunning):
				s.logger.Info("sync already running, scheduled sync skipped")
			case err != nil:
				s.logger.Error("start scheduled sync failed", "error", err)
			}
		case reply := <-s.triggers:
			runID, err := s.start(ctx, triggerManual)
			reply <- triggerResult{runID: runID, err: err}
		}
	}
}

// Trigger 手动触发一次同步，同步开始后立即返回它的 ID，同步在后台进行。
// 未配置目录源返回 errNoCatalogSource；已有同步在跑（包括其他实例）返回 errSyncRunning；
// Run 已经返回（服务正在关闭）时返回 503，不拖住优雅关闭。
func (s *SyncService) Trigger(ctx context.Context) (int64, error) {
	if s.source == nil {
		return 0, errNoCatalogSource
	}
	reply := make(chan triggerResult, 1)
	select {
	case s.triggers <- reply:
	case <-s.stopped:
		return 0, errShuttingDown
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	r := <-reply
	return r.runID, r.err
}

// ListRuns 最近 20 次同步，新的在前，不含警告正文。
func (s *SyncService) ListRuns(ctx context.Context) ([]repository.ListSyncRunsRow, error) {
	runs, err := s.store.ListSyncRuns(ctx, maxSyncRuns)
	if err != nil {
		return nil, fmt.Errorf("list sync runs: %w", err)
	}
	return runs, nil
}

// GetRun 一次同步的详情，含警告。
func (s *SyncService) GetRun(ctx context.Context, id int64) (repository.SyncRun, error) {
	run, err := s.store.GetSyncRun(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return repository.SyncRun{}, errSyncRunNotFound
		}
		return repository.SyncRun{}, fmt.Errorf("get sync run %d: %w", id, err)
	}
	return run, nil
}

// cleanupStale 启动时把残留的 running 改为 interrupted：拿得到同步的租约才清理（拿不到说明有同步正在跑），清理完释放。
// 失败只记日志：之后每次同步拿到租约时还会再清理。
func (s *SyncService) cleanupStale(ctx context.Context) {
	lease, ok, err := database.TryLease(ctx, s.pool, s.logger, database.LeaseSync)
	if err != nil {
		s.logger.Error("clean up stale sync runs failed", "error", err)
		return
	}
	if !ok {
		return
	}
	defer lease.Release()
	if err := s.interruptStale(lease.Context()); err != nil {
		s.logger.Error("clean up stale sync runs failed", "error", err)
	}
}

// interruptStale 把残留的 running 改为 interrupted。调用方必须持有同步的租约，这时不会有正在进行的同步。
func (s *SyncService) interruptStale(ctx context.Context) error {
	n, err := s.store.InterruptRunningSyncRuns(ctx)
	if err != nil {
		return fmt.Errorf("interrupt stale sync runs: %w", err)
	}
	if n > 0 {
		s.logger.Warn("stale running sync runs marked as interrupted", "count", n)
	}
	return nil
}

// start 拿租约 → 清理残留的 running → 删掉最近 20 次以前的记录 → 插入 running 记录 → 在后台执行。
// 租约由执行同步的 goroutine 持有到结束。
func (s *SyncService) start(ctx context.Context, trigger string) (int64, error) {
	lease, ok, err := database.TryLease(ctx, s.pool, s.logger, database.LeaseSync)
	if err != nil {
		return 0, fmt.Errorf("acquire sync lease: %w", err)
	}
	if !ok {
		return 0, errSyncRunning
	}

	runID, err := s.createRun(lease.Context(), trigger)
	if err != nil {
		lease.Release()
		return 0, err
	}
	s.wg.Go(func() {
		defer lease.Release()
		s.execute(lease.Context(), runID)
	})
	return runID, nil
}

func (s *SyncService) createRun(ctx context.Context, trigger string) (int64, error) {
	if err := s.interruptStale(ctx); err != nil {
		return 0, err
	}
	if err := s.store.DeleteOldSyncRuns(ctx, maxSyncRuns-1); err != nil {
		return 0, fmt.Errorf("delete old sync runs: %w", err)
	}
	runID, err := s.store.CreateSyncRun(ctx, trigger)
	if err != nil {
		return 0, fmt.Errorf("create sync run: %w", err)
	}
	return runID, nil
}

// execute 执行一次同步并写入最终状态：目录源或数据库出错记为 failed 并写明原因，已提交的部分保留，不自动重试；
// ctx 取消（关闭服务、租约丢失）记为 interrupted。最终状态用 context.WithoutCancel 加超时写入，ctx 取消后也能写完。
func (s *SyncService) execute(ctx context.Context, runID int64) {
	logger := s.logger.With("sync_run", runID)
	logger.Info("sync started")

	run := newSyncRun(runID)
	err := s.syncCatalog(ctx, run)

	status, reason, cause := statusSucceeded, (*string)(nil), context.Cause(ctx)
	switch {
	case err == nil:
	case cause != nil:
		status = statusInterrupted
	default:
		status = statusFailed
		msg := err.Error()
		reason = &msg
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveResultTimeout)
	defer cancel()
	if err := s.store.UpdateSyncRun(ctx, run.params(status, reason)); err != nil {
		logger.Error("save sync result failed", "status", status, "error", err)
	}

	level, attrs := slog.LevelInfo, []any{"status", status}
	if run.total != nil {
		attrs = append(attrs, "total", *run.total)
	}
	attrs = append(attrs, "done", run.done, "created_series", run.createdSeries, "created_seasons", run.createdSeasons,
		"created_episodes", run.createdEpisodes, "warnings", run.warningCount)
	switch status {
	case statusInterrupted:
		level = slog.LevelWarn
		attrs = append(attrs, "cause", cause)
	case statusFailed:
		level = slog.LevelError
		attrs = append(attrs, "error", err)
	}
	logger.Log(ctx, level, "sync finished", attrs...)
}

// syncRun 一次同步的进度，对应 sync_runs 的一行。
type syncRun struct {
	id                                             int64
	total                                          *int32 // 列完媒体库之前为空
	done                                           int32
	createdSeries, createdSeasons, createdEpisodes int32
	warnings                                       []string // 只保留前 maxSyncWarnings 条
	warningCount                                   int32    // 警告总数
}

func newSyncRun(id int64) *syncRun {
	// warnings 列 NOT NULL，nil 切片会被 pgx 编码为 NULL
	return &syncRun{id: id, warnings: []string{}}
}

func (r *syncRun) warn(msg string) {
	r.warningCount++
	if len(r.warnings) < maxSyncWarnings {
		r.warnings = append(r.warnings, msg)
	}
}

func (r *syncRun) params(status string, reason *string) repository.UpdateSyncRunParams {
	return repository.UpdateSyncRunParams{
		ID:              r.id,
		Status:          status,
		Total:           r.total,
		Done:            r.done,
		CreatedSeries:   r.createdSeries,
		CreatedSeasons:  r.createdSeasons,
		CreatedEpisodes: r.createdEpisodes,
		Warnings:        r.warnings,
		WarningCount:    r.warningCount,
		Error:           reason,
	}
}
