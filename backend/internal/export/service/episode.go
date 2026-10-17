package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/export/dao"
	"github.com/kzw200015/danfuse/backend/internal/naming"
)

// ExportEpisode 导出一集：一个 zip（episodeZipName），里面平铺各绑定的 <绑定标题>.xml（addEpisode）。
// 集不存在时 404，这一集没有可导出的弹幕时也 404（不输出空的 zip），都不调用 open。
// zip 是边读边写的，open 在事务里调用；之后出错时返回的错误只能让调用方中止输出。
func (s *Service) ExportEpisode(ctx context.Context, id int64, open Open) error {
	return s.snapshot(ctx, func(q *dao.Queries) error {
		ep, err := q.GetEpisode(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errEpisodeNotFound
		}
		if err != nil {
			return fmt.Errorf("get episode %d: %w", id, err)
		}
		a := newArchive(episodeZipName(ep.SeriesTitle, naming.SeriesType(ep.SeriesType), ep.SeasonNumber, ep.EpisodeNumber), open)
		if err := addEpisode(ctx, q, a, "", id); err != nil {
			return fmt.Errorf("export episode %d: %w", id, err)
		}
		return a.close(errEpisodeEmpty)
	})
}
