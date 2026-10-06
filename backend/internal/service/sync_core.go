package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/repository"
)

// syncCatalog 列出目录源的清单，再一部剧一部剧地写入目录。每处理完一部剧（包括跳过的）写入一次进度。
func (s *SyncService) syncCatalog(ctx context.Context, run *syncRun) error {
	listing, err := s.catalogSource.List(ctx)
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
// 海报下载失败只记一条警告（旧海报保留，警告里是适配写的提示，完整的错误记 info 日志），不影响这部剧的其他内容。
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
		run.warn(fmt.Sprintf("%s：下载海报失败：%s", item.Name, failureReason(err)))
		s.logger.Info("download poster failed", "sync_run", run.id, "series", item.Name, "error", err)
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
	err := s.store.ExecTx(ctx, func(q *repository.Queries) error {
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
func writeSearchVectors(ctx context.Context, q *repository.Queries, seriesID int64, series catalog.Series) error {
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
func writePoster(ctx context.Context, q *repository.Queries, seriesID int64, oldID *int64, poster *catalog.Image) error {
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
