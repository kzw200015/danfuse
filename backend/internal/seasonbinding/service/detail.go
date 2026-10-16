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
