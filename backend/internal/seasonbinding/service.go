package seasonbinding

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/infra/background"
	"github.com/kzw200015/danfuse/backend/internal/infra/config"
	"github.com/kzw200015/danfuse/backend/internal/infra/database"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/seasonbindingdb"
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
	kindCollection = "collection" // 合集的季绑定：按表单在后台补建出链接绑定，可以追更
	kindFolder     = "folder"     // 文件夹的季绑定：按表单当场建出文件绑定，文件存在条目上
)

// Service 季绑定：一季与一个合集、或一个上传的弹幕文件夹的对应关系，保存着一份表单（见 GLOSSARY），
// 各集的绑定一律按表单建出，放在季绑定在那一集的槽位里（binding.Service 的 XxxSlotInTx）。
// 每个条目按集号对应落到哪一集、槽位要新建还是替换，都由派生函数 Derive 算出，输入的组装见 derive_input.go。
//
// 文件划分：service.go 是详情、剧详情里的列表、删除、开关追更和按种类分派的立即补建；
// collection.go、backfill.go 是合集（列出、创建、后台补建与追更），folder.go 是文件夹；派生在 derive.go（纯计算）与 derive_input.go（组装输入）；JSON 的形状在 view.go。
//
// 后台补建的生命周期同同步（catalog.SyncService）：App.Run 运行 Run(ctx)（循环是 background.Loop），补建都用 Run 的 ctx，
// 不用 HTTP 请求的 ctx；ctx 取消时进行中的扫描和补建停下，Run 等它们返回，之后 main 才关闭连接池。
// "补建中"以按季绑定的租约（database.LeaseSeasonBackfill）为准，多实例同样成立，季绑定上不存运行状态。
// 网络请求都在事务之外；写入事务的第一句锁季，再锁季绑定，写一集的绑定之前先锁那一集。不死锁靠先锁季：
// 删季的第一步也是锁季，两边在这里排队，之后的顺序不必与删季的级联（季 → 集 → 季绑定 → 绑定）一致。
type Service struct {
	pool     *pgxpool.Pool // 开事务，拿按季绑定的租约
	q        *seasonbindingdb.Queries
	sources  *source.Registry
	bindings *binding.Service // 槽位里的绑定由它写入
	follow   config.Follow    // 追更的时间规则
	logger   *slog.Logger
	loop     *background.Loop // Run 的循环：追更的扫描与手动触发，进行中的扫描和补建
}

func NewService(pool *pgxpool.Pool, sources *source.Registry, bindings *binding.Service, follow config.Follow, logger *slog.Logger) *Service {
	return &Service{
		pool:     pool,
		q:        seasonbindingdb.New(pool),
		sources:  sources,
		bindings: bindings,
		follow:   follow,
		logger:   logger,
		loop:     background.NewLoop(),
	}
}

// Get 季绑定的详情：季绑定的 JSON，加上各条目按补建的语义派生的结果（不删除槽位），以及没有条目对到、绑定还留着的槽位。
// 不请求平台：合集的条目是上次列出的结果，按季绑定现在的集号规则编号。不存在时返回 404。
func (s *Service) Get(ctx context.Context, id int64) (Detail, error) {
	row, err := s.q.GetSeasonBindingSummary(ctx, seasonbindingdb.GetSeasonBindingSummaryParams{
		ID: id, LeasePrefix: database.LeaseSeasonBackfillPrefix,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Detail{}, errSeasonBindingNotFound
		}
		return Detail{}, fmt.Errorf("get season binding %d: %w", id, err)
	}
	view, err := s.view(row.SeasonBinding, row.BindingCount, row.EntryCount, row.Running)
	if err != nil {
		return Detail{}, err
	}
	in, err := storedInput(ctx, s.q, row.SeasonBinding)
	if err != nil {
		return Detail{}, err
	}
	r := Derive(in.entries, in.mapping, in.episodes)
	items := itemViews(r.Items)
	for i, it := range r.Items {
		items[i].ItemError = new(in.errors[it.Entry.Source]) // 文件夹没有失败原因，恒为 null
	}
	return Detail{View: view, Items: items, EmptySlots: slotViews(r.Empty)}, nil
}

// ListBySeries 一部剧的全部季绑定（不含条目表，两种季绑定都在内），按季 ID 分组，组内按创建顺序。剧详情用。
func (s *Service) ListBySeries(ctx context.Context, seriesID int64) (map[int64][]View, error) {
	rows, err := s.q.ListSeasonBindingSummariesBySeries(ctx, seasonbindingdb.ListSeasonBindingSummariesBySeriesParams{
		SeriesID: seriesID, LeasePrefix: database.LeaseSeasonBackfillPrefix,
	})
	if err != nil {
		return nil, fmt.Errorf("list season bindings of series %d: %w", seriesID, err)
	}
	views := make(map[int64][]View) // 季 ID → 这一季的季绑定
	for _, row := range rows {
		v, err := s.view(row.SeasonBinding, row.BindingCount, row.EntryCount, row.Running)
		if err != nil {
			return nil, err
		}
		views[row.SeasonBinding.SeasonID] = append(views[row.SeasonBinding.SeasonID], v)
	}
	return views, nil
}

// SetFollow 开关追更（只有合集的季绑定有），不删绑定、不拿租约；打开时随即在后台补建一次（正在补建时不另起一轮）。
// 不存在时 404，文件夹的季绑定 400。返回详情。
func (s *Service) SetFollow(ctx context.Context, id int64, follow bool) (Detail, error) {
	if _, err := s.getCollection(ctx, id); err != nil {
		return Detail{}, err
	}
	n, err := s.q.SetSeasonBindingFollow(ctx, seasonbindingdb.SetSeasonBindingFollowParams{ID: id, Follow: follow})
	if err != nil {
		return Detail{}, fmt.Errorf("set follow of season binding %d: %w", id, err)
	}
	if n == 0 {
		return Detail{}, errSeasonBindingNotFound
	}
	if follow {
		s.startBackfill(ctx, id)
	}
	return s.Get(ctx, id)
}

// Backfill 立即补建，按种类分派：合集在后台补建（正在补建时 409"正在补建"，不排第二次），返回时补建刚开始；
// 文件夹当场补建完（见 backfillFolder）。都返回详情。不存在时 404。
func (s *Service) Backfill(ctx context.Context, id int64) (Detail, error) {
	sb, err := s.getSeasonBinding(ctx, id, errSeasonBindingNotFound)
	if err != nil {
		return Detail{}, err
	}
	switch sb.Kind {
	case kindCollection:
		err = s.trigger(ctx, id)
	case kindFolder:
		err = s.backfillFolder(ctx, sb)
	default:
		err = fmt.Errorf("season binding %d: unknown kind %q", id, sb.Kind)
	}
	if err != nil {
		return Detail{}, err
	}
	return s.Get(ctx, id)
}

// Delete 删除季绑定，两种一样，不拿租约：一个事务里先锁住季和它（lockSeasonBinding；进行中的补建写入事务先提交，
// 之后补建再也锁不到它，随即结束），再删掉它；建出的绑定（连同弹幕）、条目表、条目和条目的文件随外键级联删除。
// 不存在时返回 404（删除前季被删除也是）。
func (s *Service) Delete(ctx context.Context, id int64) error {
	sb, err := s.getSeasonBinding(ctx, id, errSeasonBindingNotFound)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := lockSeasonBinding(ctx, q, sb.SeasonID, id); err != nil {
			return err
		}
		if err := q.DeleteSeasonBinding(ctx, id); err != nil {
			return fmt.Errorf("delete season binding %d: %w", id, err)
		}
		return nil
	})
}

// getSeasonBinding 读季绑定，不存在时返回 gone（管理 API 为 404，补建时为 errSeasonBindingGone）。
func (s *Service) getSeasonBinding(ctx context.Context, id int64, gone error) (seasonbindingdb.SeasonBinding, error) {
	sb, err := s.q.GetSeasonBinding(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return seasonbindingdb.SeasonBinding{}, gone
	}
	if err != nil {
		return seasonbindingdb.SeasonBinding{}, fmt.Errorf("get season binding %d: %w", id, err)
	}
	return sb, nil
}

// getCollection 读合集的季绑定：不存在时 404，文件夹的季绑定 400（种类不符）。种类建出后不变，在事务之外判断即可。
func (s *Service) getCollection(ctx context.Context, id int64) (seasonbindingdb.SeasonBinding, error) {
	return s.getKind(ctx, id, kindCollection, errNotCollection)
}

// getKind 读种类为 kind 的季绑定：不存在时 404，种类不符时返回 mismatch（400）。
func (s *Service) getKind(ctx context.Context, id int64, kind string, mismatch error) (seasonbindingdb.SeasonBinding, error) {
	sb, err := s.getSeasonBinding(ctx, id, errSeasonBindingNotFound)
	if err == nil && sb.Kind != kind {
		return seasonbindingdb.SeasonBinding{}, mismatch
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
func lockSeason(ctx context.Context, q *seasonbindingdb.Queries, id int64, gone error) error {
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
func lockSeasonBinding(ctx context.Context, q *seasonbindingdb.Queries, seasonID, id int64) (seasonbindingdb.SeasonBinding, error) {
	if err := lockSeason(ctx, q, seasonID, errSeasonBindingNotFound); err != nil {
		return seasonbindingdb.SeasonBinding{}, err
	}
	sb, err := q.LockSeasonBindingForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return seasonbindingdb.SeasonBinding{}, errSeasonBindingNotFound
	}
	if err != nil {
		return seasonbindingdb.SeasonBinding{}, fmt.Errorf("lock season binding %d: %w", id, err)
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
