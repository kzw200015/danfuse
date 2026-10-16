package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/infra/database"
	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/dao"
)

// Get 季绑定的详情：季绑定的 JSON，加上各条目按补建的语义派生的结果（不删除槽位），以及没有条目对到、绑定还留着的槽位。
// 不请求平台：合集的条目是上次列出的结果，按季绑定现在的集号规则编号。不存在时返回 404。
func (s *Service) Get(ctx context.Context, id int64) (Detail, error) {
	row, err := s.q.GetSeasonBindingSummary(ctx, dao.GetSeasonBindingSummaryParams{
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
	rows, err := s.q.ListSeasonBindingSummariesBySeries(ctx, dao.ListSeasonBindingSummariesBySeriesParams{
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
	n, err := s.q.SetSeasonBindingFollow(ctx, dao.SetSeasonBindingFollowParams{ID: id, Follow: follow})
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
	case KindCollection:
		err = s.trigger(ctx, id)
	case KindFolder:
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
