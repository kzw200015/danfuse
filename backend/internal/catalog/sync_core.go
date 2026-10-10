package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/catalog/catalogdb"
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

// saveProgress 写入进度。保存的警告没有新增时不重写 warnings 列（最多 maxSyncWarnings 条，每部剧都重写一遍不划算）。
func (s *SyncService) saveProgress(ctx context.Context, run *syncRun) error {
	p := run.params(statusRunning, nil)
	if len(run.warnings) == run.savedWarnings {
		p.Warnings = nil
	}
	if err := s.q.UpdateSyncRun(ctx, p); err != nil {
		return fmt.Errorf("保存同步进度失败：%w", err)
	}
	run.savedWarnings = len(run.warnings)
	return nil
}

// syncItem 处理一部剧：适配给的警告和校验的警告都加上剧名；Series 为 nil 或校验不通过的整部跳过，否则写入目录。
// 海报下载失败只记一条警告（旧海报保留，警告里是适配写的提示，完整的错误记 info 日志），不影响这部剧的其他内容。
func (s *SyncService) syncItem(ctx context.Context, run *syncRun, item Item) error {
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

// writeSeries 同步核心，每部剧一个事务：按身份写入剧（见 upsertSeries），再按季号依次 upsert 季、每季一条语句 upsert 它的集，
// 键以外的字段用目录源的数据覆盖（没变的集不重写），重算这部剧所有季的搜索列，最后按 sha256 处理海报（下载失败的保留旧海报）。
// 同一个身份出现多次时后写的覆盖先写的。返回这个事务里新增的剧、季、集数量。
// 网络请求都在事务之外：适配在交出这部剧之前已经取完了它的季和集、下载完了海报。
func (s *SyncService) writeSeries(ctx context.Context, series Series) (createdCounts, error) {
	var created createdCounts
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		seriesRow, seriesCreated, err := upsertSeries(ctx, q, series)
		if err != nil {
			return err
		}
		if seriesCreated {
			created.series++
		}

		for _, season := range series.Seasons {
			seasonRow, err := q.UpsertSeason(ctx, catalogdb.UpsertSeasonParams{
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

			n, err := upsertEpisodes(ctx, q, seasonRow.ID, season.Episodes)
			if err != nil {
				return fmt.Errorf("upsert episodes of season %d: %w", season.Number, err)
			}
			created.episodes += n
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

// upsertEpisodes 一条语句写入一季的集（见 UpsertEpisodes），返回新增的集数。集号相同的以后出现的为准。
func upsertEpisodes(ctx context.Context, q *catalogdb.Queries, seasonID int64, episodes []Episode) (int32, error) {
	p := catalogdb.UpsertEpisodesParams{SeasonID: seasonID}
	index := make(map[int]int, len(episodes)) // 集号 → 在参数数组里的下标
	for _, ep := range episodes {
		duration := int32(-1) // 存为 null
		if ep.Duration != nil {
			duration = int32(*ep.Duration)
		}
		if i, ok := index[ep.Number]; ok {
			p.Titles[i], p.Durations[i] = ep.Title, duration
			continue
		}
		index[ep.Number] = len(p.Numbers)
		p.Numbers = append(p.Numbers, int32(ep.Number))
		p.Titles = append(p.Titles, ep.Title)
		p.Durations = append(p.Durations, duration)
	}
	return q.UpsertEpisodes(ctx, p)
}

// upsertSeries 按身份写入一部剧（见 docs/adr/0007），返回写入后的行和是否新增：
//  1. 有 TMDB ID 时先按（类型，TMDB ID）找，找到就覆盖标题、年份和原名；
//  2. 没找到（或没有 TMDB ID）再按（类型，标题，年份）在没有 TMDB ID 的剧里找，找到就覆盖原名、补上 TMDB ID。
//     只在没有 TMDB ID 的剧里找：已有 TMDB ID 的剧标题年份相同也是另一部，在 Jellyfin 里重新识别成另一个条目按新剧处理；
//  3. 都没找到就新增。
//
// 分三步而不是一条 ON CONFLICT：同步持有租约，没有并发的写入者；同步进行中删除的剧，下一步按新行处理即可。
func upsertSeries(ctx context.Context, q *catalogdb.Queries, series Series) (catalogdb.Series, bool, error) {
	if series.TMDBID != nil {
		row, err := q.UpdateSeriesByTMDBID(ctx, catalogdb.UpdateSeriesByTMDBIDParams{
			Type:          string(series.Type),
			TmdbID:        *series.TMDBID,
			Title:         series.Title,
			OriginalTitle: nullIfEmpty(series.OriginalTitle),
			Year:          int32Ptr(series.Year),
		})
		if err == nil {
			return row, false, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return catalogdb.Series{}, false, fmt.Errorf("update series by tmdb id: %w", err)
		}
	}

	row, err := q.AdoptSeriesByNaturalKey(ctx, catalogdb.AdoptSeriesByNaturalKeyParams{
		Type:          string(series.Type),
		Title:         series.Title,
		Year:          int32Ptr(series.Year),
		OriginalTitle: nullIfEmpty(series.OriginalTitle),
		TmdbID:        series.TMDBID,
	})
	if err == nil {
		return row, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return catalogdb.Series{}, false, fmt.Errorf("update series by natural key: %w", err)
	}

	row, err = q.InsertSeries(ctx, catalogdb.InsertSeriesParams{
		Type:          string(series.Type),
		Title:         series.Title,
		OriginalTitle: nullIfEmpty(series.OriginalTitle),
		Year:          int32Ptr(series.Year),
		TmdbID:        series.TMDBID,
	})
	if err != nil {
		return catalogdb.Series{}, false, fmt.Errorf("insert series: %w", err)
	}
	return row, true, nil
}

// writeSearchVectors 重算一部剧所有季的搜索列，在这部剧的事务里调用。包括这次目录源没有给出的季：
// 剧名、原名是每一季搜索列的一部分，原名变了每一季都要重算。
func writeSearchVectors(ctx context.Context, q *catalogdb.Queries, seriesID int64, series Series) error {
	seasons, err := q.ListSeasonsBySeries(ctx, seriesID)
	if err != nil {
		return fmt.Errorf("list seasons: %w", err)
	}
	p := catalogdb.SetSeasonSearchVectorsParams{Ids: make([]int64, len(seasons)), Vectors: make([]string, len(seasons))}
	for i, se := range seasons {
		p.Ids[i] = se.ID
		p.Vectors[i] = SearchVector(series.Type, series.Title, series.OriginalTitle, int(se.Number), emptyIfNull(se.Title))
	}
	if err := q.SetSeasonSearchVectors(ctx, p); err != nil {
		return fmt.Errorf("set search vectors: %w", err)
	}
	return nil
}

// writePoster 按 sha256 处理一部剧的海报，在这部剧的事务里调用（upsert 已锁住剧这一行）。
// poster 为 nil 表示目录源里没有图，oldID 是剧现在的海报。每张图只被一部剧引用，换下来的旧图直接删除：
//   - 与旧图的 sha256 相同：不写；
//   - 不同（或原来没有海报）：插入新图 → 剧指向新图 → 删除旧图；
//   - 没有图：清空剧的海报 → 删除旧图。
func writePoster(ctx context.Context, q *catalogdb.Queries, seriesID int64, oldID *int64, poster *Image) error {
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
		id, err := q.InsertImage(ctx, catalogdb.InsertImageParams{
			ContentType: poster.ContentType,
			Data:        poster.Data,
			Sha256:      sum[:],
		})
		if err != nil {
			return fmt.Errorf("insert poster: %w", err)
		}
		newID = &id
	}

	if err := q.SetSeriesPoster(ctx, catalogdb.SetSeriesPosterParams{ID: seriesID, PosterImageID: newID}); err != nil {
		return fmt.Errorf("set poster: %w", err)
	}
	if oldID != nil {
		if err := q.DeleteImage(ctx, *oldID); err != nil {
			return fmt.Errorf("delete poster %d: %w", *oldID, err)
		}
	}
	return nil
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
