// Package service 季绑定的业务层：合集与文件夹的季绑定、派生、补建与追更，文件划分见 Service 的注释。
// HTTP 在同级的 handler 包，查询在 dao 包（sqlc）。
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	bindingsvc "github.com/kzw200015/danfuse/backend/internal/binding/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/background"
	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/dao"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	errSeasonNotFound        = apierr.ErrNotFound.WithMessage("季不存在")
	errSeasonDeleted         = apierr.ErrNotFound.WithMessage("这一季已被删除")
	errSeasonBindingNotFound = apierr.ErrNotFound.WithMessage("季绑定不存在")
	errBackfillRunning       = apierr.ErrConflict.WithMessage("正在补建")
	errNotCollection         = apierr.ErrBadRequest.WithMessage("这不是合集的季绑定")
)

// 季绑定的种类（season_bindings.kind）。
const (
	KindCollection = "collection" // 合集的季绑定：按表单在后台补建出链接绑定，可以追更
	KindFolder     = "folder"     // 文件夹的季绑定：按表单当场建出文件绑定，文件存在条目上
)

// Service 季绑定：一季与一个合集、或一个上传的弹幕文件夹的对应关系，保存着一份表单（见 GLOSSARY），
// 各集的绑定一律按表单建出，放在季绑定在那一集的槽位里（binding.Service 的 XxxSlotInTx）。
// 每个条目按集号对应落到哪一集、槽位要新建还是替换，都由派生函数 Derive 算出，输入的组装见 derive_input.go。
//
// 文件划分：service.go 是 Service、错误与共用的取、锁；两种共用的操作按功能分在 detail.go（详情、剧详情里的列表）、
// follow.go（开关追更）、delete.go（删除），按种类分派的立即补建在 backfill.go；
// collection.go、backfill.go 是合集（列出、创建、后台补建与追更），folder.go 是文件夹；派生在 derive.go（纯计算）与 derive_input.go（组装输入）；JSON 的形状在 view.go。
//
// 后台补建的生命周期同同步（catalog.SyncService）：App.Run 运行 Run(ctx)（循环是 background.Loop），补建都用 Run 的 ctx，
// 不用 HTTP 请求的 ctx；ctx 取消时进行中的扫描和补建停下，Run 等它们返回，之后 main 才关闭连接池。
// "补建中"以按季绑定的租约（database.LeaseSeasonBackfill）为准，多实例同样成立，季绑定上不存运行状态。
// 网络请求都在事务之外；写入事务的第一句锁季，再锁季绑定，写一集的绑定之前先锁那一集。不死锁靠先锁季：
// 删季的第一步也是锁季，两边在这里排队，之后的顺序不必与删季的级联（季 → 集 → 季绑定 → 绑定）一致。
type Service struct {
	pool     *pgxpool.Pool // 开事务，拿按季绑定的租约
	q        *dao.Queries
	sources  *source.Registry
	bindings *bindingsvc.Service // 槽位里的绑定由它写入
	follow   config.Follow       // 追更的时间规则
	logger   *slog.Logger
	loop     *background.Loop // Run 的循环：追更的扫描与手动触发，进行中的扫描和补建
}

func NewService(pool *pgxpool.Pool, sources *source.Registry, bindings *bindingsvc.Service, follow config.Follow, logger *slog.Logger) *Service {
	return &Service{
		pool:     pool,
		q:        dao.New(pool),
		sources:  sources,
		bindings: bindings,
		follow:   follow,
		logger:   logger,
		loop:     background.NewLoop(),
	}
}

// getSeasonBinding 读季绑定，不存在时返回 gone（管理 API 为 404，补建时为 errSeasonBindingGone）。
func (s *Service) getSeasonBinding(ctx context.Context, id int64, gone error) (dao.SeasonBinding, error) {
	sb, err := s.q.GetSeasonBinding(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return dao.SeasonBinding{}, gone
	}
	if err != nil {
		return dao.SeasonBinding{}, fmt.Errorf("get season binding %d: %w", id, err)
	}
	return sb, nil
}

// getCollection 读合集的季绑定：不存在时 404，文件夹的季绑定 400（种类不符）。种类建出后不变，在事务之外判断即可。
func (s *Service) getCollection(ctx context.Context, id int64) (dao.SeasonBinding, error) {
	return s.getKind(ctx, id, KindCollection, errNotCollection)
}

// getKind 读种类为 kind 的季绑定：不存在时 404，种类不符时返回 mismatch（400）。
func (s *Service) getKind(ctx context.Context, id int64, kind string, mismatch error) (dao.SeasonBinding, error) {
	sb, err := s.getSeasonBinding(ctx, id, errSeasonBindingNotFound)
	if err == nil && sb.Kind != kind {
		return dao.SeasonBinding{}, mismatch
	}
	return sb, err
}

// checkSeason 确认这一季存在，请求平台、解析文件之前就能返回 404。
func (s *Service) checkSeason(ctx context.Context, seasonID int64) error {
	switch exists, err := s.q.SeasonExists(ctx, seasonID); {
	case err != nil:
		return fmt.Errorf("check season %d: %w", seasonID, err)
	case !exists:
		return errSeasonNotFound
	}
	return nil
}

// lockSeason 写入事务的第一句：锁住一季到提交（FOR KEY SHARE），期间删不掉它；这一季已被删除时返回 gone。
func lockSeason(ctx context.Context, q *dao.Queries, id int64, gone error) error {
	if _, err := q.LockSeason(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gone
		}
		return fmt.Errorf("lock season %d: %w", id, err)
	}
	return nil
}

// lockSeasonBinding 保存表单、删除和文件夹的写入事务的前两句：锁季（lockSeason），再 FOR UPDATE 锁季绑定，
// 与进行中的补建写入、另一次写入排队；返回锁住时的季绑定。季或季绑定已被删除时 404"季绑定不存在"（季删除时季绑定随之删除）。
func lockSeasonBinding(ctx context.Context, q *dao.Queries, seasonID, id int64) (dao.SeasonBinding, error) {
	if err := lockSeason(ctx, q, seasonID, errSeasonBindingNotFound); err != nil {
		return dao.SeasonBinding{}, err
	}
	sb, err := q.LockSeasonBindingForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return dao.SeasonBinding{}, errSeasonBindingNotFound
	}
	if err != nil {
		return dao.SeasonBinding{}, fmt.Errorf("lock season binding %d: %w", id, err)
	}
	return sb, nil
}

// emptyIfNull null 读作空串。
func emptyIfNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
