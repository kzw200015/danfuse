package service

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	ErrSyncRunning     = errcode.ErrConflict.WithMessage("同步正在进行")
	ErrNoCatalogSource = errcode.ErrConflict.WithMessage("未配置目录源")
	errSyncRunNotFound = errcode.ErrNotFound.WithMessage("同步记录不存在")
	errShuttingDown    = errcode.ErrServiceUnavailable.WithMessage("服务正在关闭")
)

// SyncService 同步：触发、定时、互斥、同步核心（按剧写入目录）、同步记录。
//
// 生命周期：App.Run 运行 Run(ctx)；手动触发经 Trigger 交给 Run 的循环。每次同步在独立的 goroutine 里执行，
// 用的是 Run 的 ctx（应用级），不是 HTTP 请求的 ctx。ctx 取消时这次同步记为 interrupted，Run 等它写完记录再返回，
// 之后 wire 的 cleanup 才关闭连接池。
//
// 互斥靠 PostgreSQL 的 advisory lock（database.LockSync），多实例同样成立：每次同步用一个专用连接持有锁直到结束。
type SyncService struct {
	store    repository.Store
	pool     *pgxpool.Pool  // 取专用连接持有同步锁
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
			case errors.Is(err, ErrSyncRunning):
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
// 未配置目录源返回 ErrNoCatalogSource；已有同步在跑（包括其他实例）返回 ErrSyncRunning；
// Run 已经返回（服务正在关闭）时返回 503，不拖住优雅关闭。
func (s *SyncService) Trigger(ctx context.Context) (int64, error) {
	if s.source == nil {
		return 0, ErrNoCatalogSource
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

// cleanupStale 启动时把残留的 running 改为 interrupted：拿得到同步锁才清理（拿不到说明有同步正在跑），清理完释放锁。
// 失败只记日志：之后每次同步拿到锁时还会再清理。
func (s *SyncService) cleanupStale(ctx context.Context) {
	unlock, ok, err := database.TryAdvisoryLock(ctx, s.pool, database.LockSync)
	if err != nil {
		s.logger.Error("clean up stale sync runs failed", "error", err)
		return
	}
	if !ok {
		return
	}
	defer unlock()
	if err := s.interruptStale(ctx); err != nil {
		s.logger.Error("clean up stale sync runs failed", "error", err)
	}
}

// interruptStale 把残留的 running 改为 interrupted。调用方必须持有同步锁，这时不会有正在进行的同步。
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

// start 拿锁 → 清理残留的 running → 删掉最近 20 次以前的记录 → 插入 running 记录 → 在后台执行。
// 锁由执行同步的 goroutine 持有到结束。
func (s *SyncService) start(ctx context.Context, trigger string) (int64, error) {
	unlock, ok, err := database.TryAdvisoryLock(ctx, s.pool, database.LockSync)
	if err != nil {
		return 0, fmt.Errorf("acquire sync lock: %w", err)
	}
	if !ok {
		return 0, ErrSyncRunning
	}

	runID, err := s.createRun(ctx, trigger)
	if err != nil {
		unlock()
		return 0, err
	}
	s.wg.Go(func() {
		defer unlock()
		s.execute(ctx, runID)
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
// ctx 取消记为 interrupted。最终状态用 context.WithoutCancel 加超时写入，ctx 取消后也能写完。
func (s *SyncService) execute(ctx context.Context, runID int64) {
	logger := s.logger.With("sync_run", runID)
	logger.Info("sync started")

	run := newSyncRun(runID)
	err := s.syncCatalog(ctx, run)

	status, reason := statusSucceeded, (*string)(nil)
	switch {
	case err == nil:
	case ctx.Err() != nil:
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
	case statusFailed:
		level = slog.LevelError
		attrs = append(attrs, "error", err)
	}
	logger.Log(ctx, level, "sync finished", attrs...)
}

// syncCatalog 列出目录源的清单，再一部剧一部剧地写入目录。每处理完一部剧（包括跳过的）写入一次进度。
func (s *SyncService) syncCatalog(ctx context.Context, run *syncRun) error {
	listing, err := s.source.List(ctx)
	if err != nil {
		return err
	}
	total := int32(listing.Total)
	run.total = &total
	for _, w := range listing.Warnings {
		run.warn(w)
	}
	if err := s.saveProgress(ctx, run); err != nil {
		return err
	}

	for item, err := range listing.Items {
		if err != nil {
			return err
		}
		if err := s.syncItem(ctx, run, item); err != nil {
			return err
		}
		run.done++
		if err := s.saveProgress(ctx, run); err != nil {
			return err
		}
	}
	return nil
}

func (s *SyncService) saveProgress(ctx context.Context, run *syncRun) error {
	if err := s.store.UpdateSyncRun(ctx, run.params(statusRunning, nil)); err != nil {
		return fmt.Errorf("保存同步进度失败：%w", err)
	}
	return nil
}

// syncItem 处理一部剧：适配给的警告和校验的警告都加上剧名；Series 为 nil 或校验不通过的整部跳过，否则写入目录。
// 海报下载失败只记一条警告（旧海报保留），不影响这部剧的其他内容。
func (s *SyncService) syncItem(ctx context.Context, run *syncRun, item catalog.Item) error {
	for _, w := range item.Warnings {
		run.warn(item.Name + "：" + w)
	}
	if item.Series == nil {
		return nil
	}
	if err := item.Series.Validate(); err != nil {
		run.warn(fmt.Sprintf("%s：%v，整部跳过", item.Name, err))
		return nil
	}

	created, err := s.writeSeries(ctx, *item.Series)
	if err != nil {
		return fmt.Errorf("写入「%s」失败：%w", item.Name, err)
	}
	if err := item.Series.PosterErr; err != nil {
		run.warn(fmt.Sprintf("%s：下载海报失败：%v", item.Name, err))
	}
	run.createdSeries += created.series
	run.createdSeasons += created.seasons
	run.createdEpisodes += created.episodes
	return nil
}

type createdCounts struct{ series, seasons, episodes int32 }

// writeSeries 同步核心，每部剧一个事务：按自然键依次 upsert 剧、季、集，键以外的字段用目录源的数据覆盖，
// 重算这部剧所有季的搜索列，最后按 sha256 处理海报（下载失败的保留旧海报）。同一个自然键出现多次时后写的覆盖先写的。
// 返回这个事务里新增的剧、季、集数量。
// 网络请求都在事务之外：适配在交出这部剧之前已经取完了它的季和集、下载完了海报。
func (s *SyncService) writeSeries(ctx context.Context, series catalog.Series) (createdCounts, error) {
	var created createdCounts
	err := s.store.ExecTx(ctx, func(q repository.Querier) error {
		seriesRow, err := q.UpsertSeries(ctx, repository.UpsertSeriesParams{
			Type:          string(series.Type),
			Title:         series.Title,
			OriginalTitle: nullIfEmpty(series.OriginalTitle),
			Year:          int32Ptr(series.Year),
		})
		if err != nil {
			return fmt.Errorf("upsert series: %w", err)
		}
		if seriesRow.Created {
			created.series++
		}

		for _, season := range series.Seasons {
			seasonRow, err := q.UpsertSeason(ctx, repository.UpsertSeasonParams{
				SeriesID: seriesRow.ID,
				Number:   int32(season.Number),
				Title:    nullIfEmpty(season.Title),
			})
			if err != nil {
				return fmt.Errorf("upsert season %d: %w", season.Number, err)
			}
			if seasonRow.Created {
				created.seasons++
			}

			for _, ep := range season.Episodes {
				epRow, err := q.UpsertEpisode(ctx, repository.UpsertEpisodeParams{
					SeasonID: seasonRow.ID,
					Number:   int32(ep.Number),
					Title:    nullIfEmpty(ep.Title),
					Duration: int32Ptr(ep.Duration),
				})
				if err != nil {
					return fmt.Errorf("upsert season %d episode %d: %w", season.Number, ep.Number, err)
				}
				if epRow.Created {
					created.episodes++
				}
			}
		}

		if err := writeSearchVectors(ctx, q, seriesRow.ID, series); err != nil {
			return err
		}
		if series.PosterErr == nil { // 下载失败时保留旧海报，警告由 syncItem 记
			if err := writePoster(ctx, q, seriesRow.ID, seriesRow.PosterImageID, series.Poster); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return createdCounts{}, err
	}
	return created, nil
}

// writeSearchVectors 重算一部剧所有季的搜索列，在这部剧的事务里调用。包括这次目录源没有给出的季：
// 剧名、原名是每一季搜索列的一部分，原名变了每一季都要重算。
func writeSearchVectors(ctx context.Context, q repository.Querier, seriesID int64, series catalog.Series) error {
	seasons, err := q.ListSeasonsBySeries(ctx, seriesID)
	if err != nil {
		return fmt.Errorf("list seasons: %w", err)
	}
	for _, se := range seasons {
		vector := catalog.SearchVector(series.Type, series.Title, series.OriginalTitle, int(se.Number), emptyIfNull(se.Title))
		if err := q.SetSeasonSearchVector(ctx, repository.SetSeasonSearchVectorParams{ID: se.ID, SearchVector: vector}); err != nil {
			return fmt.Errorf("set search vector of season %d: %w", se.Number, err)
		}
	}
	return nil
}

// writePoster 按 sha256 处理一部剧的海报，在这部剧的事务里调用（upsert 已锁住剧这一行）。
// poster 为 nil 表示目录源里没有图，oldID 是剧现在的海报。每张图只被一部剧引用，换下来的旧图直接删除：
//   - 与旧图的 sha256 相同：不写；
//   - 不同（或原来没有海报）：插入新图 → 剧指向新图 → 删除旧图；
//   - 没有图：清空剧的海报 → 删除旧图。
func writePoster(ctx context.Context, q repository.Querier, seriesID int64, oldID *int64, poster *catalog.Image) error {
	if poster == nil && oldID == nil {
		return nil
	}
	var newID *int64
	if poster != nil {
		sum := sha256.Sum256(poster.Data)
		if oldID != nil {
			oldSum, err := q.GetImageSHA256(ctx, *oldID)
			if err != nil {
				return fmt.Errorf("get poster %d: %w", *oldID, err)
			}
			if bytes.Equal(oldSum, sum[:]) {
				return nil
			}
		}
		id, err := q.InsertImage(ctx, repository.InsertImageParams{
			ContentType: poster.ContentType,
			Data:        poster.Data,
			Sha256:      sum[:],
		})
		if err != nil {
			return fmt.Errorf("insert poster: %w", err)
		}
		newID = &id
	}

	if err := q.SetSeriesPoster(ctx, repository.SetSeriesPosterParams{ID: seriesID, PosterImageID: newID}); err != nil {
		return fmt.Errorf("set poster: %w", err)
	}
	if oldID != nil {
		if err := q.DeleteImage(ctx, *oldID); err != nil {
			return fmt.Errorf("delete poster %d: %w", *oldID, err)
		}
	}
	return nil
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

// nullIfEmpty 空串存为 null。
func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// emptyIfNull null 读作空串。
func emptyIfNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	return new(int32(*v))
}
