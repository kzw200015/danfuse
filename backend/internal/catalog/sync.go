package catalog

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/background"
	"github.com/kzw200015/danfuse/backend/internal/catalog/catalogdb"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
)

const (
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
	errSyncRunning     = apierr.ErrConflict.WithMessage("同步正在进行")
	errNoCatalogSource = apierr.ErrConflict.WithMessage("未配置目录源")
	errSyncRunNotFound = apierr.ErrNotFound.WithMessage("同步记录不存在")

	// errSyncNotDue 定时同步拿到租约之后发现已经不到期：判定到期之后、拿到租约之前，其他实例刚做完一次同步。
	errSyncNotDue = errors.New("scheduled sync not due")
)

// internalErrorMessage 同步遇到服务器内部错误时存在同步记录上给管理界面看的原因，完整的错误进日志。
const internalErrorMessage = "服务器内部错误，详见日志"

// SyncService 同步：触发、定时、互斥、同步核心（按剧写入目录，见 sync_core.go）、同步记录。
//
// 生命周期：App.Run 运行 Run(ctx)；手动触发经 Trigger 交给 Run 的循环（background.Loop），同步开始后返回。每次同步在独立的 goroutine 里执行，
// 用的是 Run 的 ctx（应用级），不是 HTTP 请求的 ctx。ctx 取消时这次同步记为 interrupted，Run 等它写完记录再返回，
// 之后 main 才关闭连接池。
//
// 互斥靠同步的租约（database.LeaseSync），多实例同样成立：每次同步持有租约直到结束，用租约的 ctx 执行，
// 租约丢失时这次同步同样记为 interrupted。已有同步在跑时，定时的触发被丢弃、只记日志，手动的触发返回 409。
type SyncService struct {
	pool          *pgxpool.Pool // 开事务，拿同步的租约
	q             *catalogdb.Queries
	catalogSource Source // nil 表示未配置目录源
	interval      time.Duration
	keepRuns      int32 // 同步记录保留最近几次
	logger        *slog.Logger
	loop          *background.Loop // Run 的循环：定时与手动触发，进行中的同步
}

func NewSyncService(pool *pgxpool.Pool, catalogSource Source, cfg config.Sync, logger *slog.Logger) *SyncService {
	return &SyncService{
		pool:          pool,
		q:             catalogdb.New(pool),
		catalogSource: catalogSource,
		interval:      cfg.Interval,
		keepRuns:      cfg.KeepRuns,
		logger:        logger,
		loop:          background.NewLoop(),
	}
}

// Run 后台循环，阻塞到 ctx 取消；返回前等进行中的同步写完最终状态。只能调用一次。
// 启动时先清理残留的 running；配置了目录源且 interval > 0 时，距最近一次同步（含手动触发的，按开始时间）满一个间隔就定时同步：
// 下一次的时间从同步记录算，重启不会推迟，启动时已经到期（或从没同步过）就立即同步。
func (s *SyncService) Run(ctx context.Context) {
	s.cleanupStale(ctx)

	if s.catalogSource == nil || s.interval <= 0 {
		s.loop.Run(ctx, nil, nil)
		return
	}
	timer := time.NewTimer(s.untilDue(ctx))
	defer timer.Stop()
	s.loop.Run(ctx, timer.C, func(ctx context.Context) {
		// 到点时再算一次：定时器设下之后可能有过手动触发的同步。开始了的话距下一次正好一个间隔，
		// 没开始（其他实例或手动触发的同步在跑）时从那次同步算；仍然到期（开始失败）时等一个间隔再试，不连续重试
		d := s.untilDue(ctx)
		if d <= 0 {
			s.startScheduled(ctx)
			if d = s.untilDue(ctx); d <= 0 {
				d = s.interval
			}
		}
		timer.Reset(d)
	})
}

// untilDue 距下一次定时同步还有多久：最近一次同步的开始时间加一个间隔，减去现在；已经到期时不大于 0，从没同步过时为 0。
// 读不到同步记录时记日志、按一个间隔算。
func (s *SyncService) untilDue(ctx context.Context) time.Duration {
	run, err := s.LatestRun(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Error("schedule sync failed", "error", err)
		}
		return s.interval
	}
	if run == nil {
		return 0
	}
	return time.Until(run.StartedAt.Add(s.interval))
}

// Trigger 手动触发一次同步，同步开始后立即返回它的 ID，同步在后台进行。
// 未配置目录源返回 errNoCatalogSource；已有同步在跑（包括其他实例）返回 errSyncRunning；
// Run 已经返回（服务正在关闭）时返回 background.ErrStopped（503），不拖住优雅关闭。
func (s *SyncService) Trigger(ctx context.Context) (int64, error) {
	if s.catalogSource == nil {
		return 0, errNoCatalogSource
	}
	var runID int64
	err := s.loop.Call(ctx, func(ctx context.Context) error {
		var err error
		runID, err = s.tryStart(ctx, triggerManual)
		return err
	})
	return runID, err
}

// ListRuns 最近 sync.keep_runs 次同步，新的在前，不含警告正文。
func (s *SyncService) ListRuns(ctx context.Context) ([]catalogdb.ListSyncRunsRow, error) {
	runs, err := s.q.ListSyncRuns(ctx, s.keepRuns)
	if err != nil {
		return nil, fmt.Errorf("list sync runs: %w", err)
	}
	return runs, nil
}

// LatestRun 最近一次同步，不含警告正文；一次都没有同步过时为 nil。
func (s *SyncService) LatestRun(ctx context.Context) (*catalogdb.ListSyncRunsRow, error) {
	runs, err := s.q.ListSyncRuns(ctx, 1)
	if err != nil {
		return nil, fmt.Errorf("get latest sync run: %w", err)
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return &runs[0], nil
}

// GetRun 一次同步的详情，含警告。
func (s *SyncService) GetRun(ctx context.Context, id int64) (catalogdb.SyncRun, error) {
	run, err := s.q.GetSyncRun(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return catalogdb.SyncRun{}, errSyncRunNotFound
		}
		return catalogdb.SyncRun{}, fmt.Errorf("get sync run %d: %w", id, err)
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
	n, err := s.q.InterruptRunningSyncRuns(ctx)
	if err != nil {
		return fmt.Errorf("interrupt stale sync runs: %w", err)
	}
	if n > 0 {
		s.logger.Warn("stale running sync runs marked as interrupted", "count", n)
	}
	return nil
}

// startScheduled 开始一次定时同步，出错（包括已有同步在跑）只记日志；拿到租约之后已经不到期时什么都不做。
func (s *SyncService) startScheduled(ctx context.Context) {
	switch _, err := s.tryStart(ctx, triggerSchedule); {
	case errors.Is(err, errSyncNotDue):
	case errors.Is(err, errSyncRunning):
		s.logger.Info("sync already running, skipped", "trigger", triggerSchedule)
	case err != nil && ctx.Err() == nil:
		s.logger.Error("start sync failed", "trigger", triggerSchedule, "error", err)
	}
}

// tryStart 拿租约 → 清理残留的 running → 删掉最近 sync.keep_runs 次以前的记录 → 插入 running 记录 → 在后台执行，返回这次同步的 ID。
// 租约由执行同步的 goroutine 持有到结束；拿不到租约时返回 errSyncRunning。
// 定时触发拿到租约之后再确认一次仍然到期，不到期时返回 errSyncNotDue，多实例同时到点时不会接连同步两次。
func (s *SyncService) tryStart(ctx context.Context, trigger string) (int64, error) {
	lease, ok, err := database.TryLease(ctx, s.pool, s.logger, database.LeaseSync)
	if err != nil {
		return 0, fmt.Errorf("acquire sync lease: %w", err)
	}
	if !ok {
		return 0, errSyncRunning
	}
	if trigger == triggerSchedule && s.untilDue(lease.Context()) > 0 {
		lease.Release()
		return 0, errSyncNotDue
	}

	runID, err := s.createRun(lease.Context(), trigger)
	if err != nil {
		lease.Release()
		return 0, err
	}
	s.loop.Spawn(func() {
		defer lease.Release()
		s.execute(lease.Context(), runID)
	})
	return runID, nil
}

func (s *SyncService) createRun(ctx context.Context, trigger string) (int64, error) {
	if err := s.interruptStale(ctx); err != nil {
		return 0, err
	}
	if err := s.q.DeleteOldSyncRuns(ctx, s.keepRuns-1); err != nil {
		return 0, fmt.Errorf("delete old sync runs: %w", err)
	}
	runID, err := s.q.CreateSyncRun(ctx, catalogdb.CreateSyncRunParams{Trigger: trigger, StartedAt: time.Now()})
	if err != nil {
		return 0, fmt.Errorf("create sync run: %w", err)
	}
	return runID, nil
}

// execute 执行一次同步并写入最终状态：目录源或数据库出错记为 failed 并写明原因（见 failureReason），已提交的部分保留，不自动重试；
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
		reason = new(failureReason(err))
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveResultTimeout)
	defer cancel()
	if err := s.q.UpdateSyncRun(ctx, run.params(status, reason)); err != nil {
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

// failureReason 同步页上显示的失败原因：目录源的错误用适配写的提示，其他错误（写库失败等）不展示细节。
// 完整的错误由 execute 记进 "sync finished" 日志。
func failureReason(err error) string {
	if catalogErr, ok := errors.AsType[*Error](err); ok {
		return catalogErr.Message
	}
	return internalErrorMessage
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

func (r *syncRun) params(status string, reason *string) catalogdb.UpdateSyncRunParams {
	return catalogdb.UpdateSyncRunParams{
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
