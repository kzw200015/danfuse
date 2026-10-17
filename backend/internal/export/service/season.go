package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/export/dao"
)

// ExportSeason 导出一季：<剧名> S01.zip，里面一层根目录 <剧名> S01/（seasonDirName），每集一个 S01E03/ 子目录（episodeCode，
// 不带集标题：集标题可能为空，也可能带数字干扰集号规则），放这一集各绑定的 <绑定标题>.xml（addEpisode）。
// 集按集号排序；没有可导出绑定的集不建子目录（zip 里不写目录项，目录只出现在文件的路径里）。
// 解压出的根目录可以直接用于按季上传，子目录名能被默认集号规则认出。电影不提供季导出的入口，但这里不区分剧的类型。
// 季不存在时 404，整季没有可导出的弹幕时也 404（不输出空的 zip），都不调用 open。
// zip 是边读边写的，open 在事务里调用；之后出错时返回的错误只能让调用方中止输出。
func (s *Service) ExportSeason(ctx context.Context, id int64, open Open) error {
	return s.snapshot(ctx, func(q *dao.Queries) error {
		season, err := q.GetSeason(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errSeasonNotFound
		}
		if err != nil {
			return fmt.Errorf("get season %d: %w", id, err)
		}
		episodes, err := q.ListEpisodesBySeason(ctx, id)
		if err != nil {
			return fmt.Errorf("list episodes of season %d: %w", id, err)
		}
		root := seasonDirName(season.SeriesTitle, season.SeasonNumber)
		a := newArchive(root+".zip", open)
		for _, ep := range episodes {
			dir := root + "/" + episodeCode(season.SeasonNumber, ep.Number) + "/"
			if err := addEpisode(ctx, q, a, dir, ep.ID); err != nil {
				return fmt.Errorf("export season %d: %w", id, err)
			}
		}
		return a.close(errSeasonEmpty)
	})
}
