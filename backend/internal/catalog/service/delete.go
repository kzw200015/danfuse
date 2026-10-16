package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// DeleteSeries 删除一部剧，连同它的海报：同一个事务里先删剧、再删图。剧不存在时返回 404。
func (s *Service) DeleteSeries(ctx context.Context, id int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		posterID, err := q.DeleteSeries(ctx, id)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errSeriesNotFound
			}
			return fmt.Errorf("delete series %d: %w", id, err)
		}
		if posterID == nil {
			return nil
		}
		if err := q.DeleteImage(ctx, *posterID); err != nil {
			return fmt.Errorf("delete poster %d of series %d: %w", *posterID, id, err)
		}
		return nil
	})
}

// DeleteSeason 删除一季，单条语句。季不存在时返回 404。
func (s *Service) DeleteSeason(ctx context.Context, id int64) error {
	n, err := s.q.DeleteSeason(ctx, id)
	if err != nil {
		return fmt.Errorf("delete season %d: %w", id, err)
	}
	if n == 0 {
		return errSeasonNotFound
	}
	return nil
}

// DeleteEpisode 删除一集，单条语句。集不存在时返回 404。
func (s *Service) DeleteEpisode(ctx context.Context, id int64) error {
	n, err := s.q.DeleteEpisode(ctx, id)
	if err != nil {
		return fmt.Errorf("delete episode %d: %w", id, err)
	}
	if n == 0 {
		return errEpisodeNotFound
	}
	return nil
}
