import type { ReactNode } from 'react'
import { useParams } from 'react-router'

import { ApiError } from '@/api/request'
import type { SeriesDetail } from '@/api/series'
import { useSeries } from '@/hooks/use-series'

import {
  catalogPath,
  parseId,
  resolveSelection,
  seasonName,
  type Selection,
} from './catalog/catalog'
import EpisodePanel from './catalog/EpisodePanel'
import SeasonPanel from './catalog/SeasonPanel'
import SeriesColumn from './catalog/SeriesColumn'
import SeriesList from './catalog/SeriesList'
import { Hint, NotFound } from './catalog/shared'

/**
 * 目录页：剧列表 | 选中的剧 | 集面板或季面板，三栏占满顶栏以下的高度。
 * 选中的剧、季、集都在地址栏里：/catalog[/:seriesId[/:seasonId[/:episodeId]]]
 */
export default function CatalogView() {
  const { seriesId, seasonId, episodeId } = useParams()
  const id = seriesId === undefined ? undefined : parseId(seriesId)

  return (
    <div className="grid h-full grid-cols-[17rem_21rem_1fr] grid-rows-1">
      <SeriesList selectedId={id} />
      {seriesId === undefined ? (
        <MiddleColumn>
          <Hint>选择一部剧</Hint>
        </MiddleColumn>
      ) : id === undefined ? (
        <MiddleColumn>
          <SeriesNotFound />
        </MiddleColumn>
      ) : (
        <SelectedSeries id={id} seasonId={seasonId} episodeId={episodeId} />
      )}
    </div>
  )
}

/** 中栏与右栏：加载选中的剧，按地址栏选中季和集 */
function SelectedSeries({
  id,
  seasonId,
  episodeId,
}: {
  id: number
  seasonId?: string
  episodeId?: string
}) {
  const { data: series, error } = useSeries(id)

  if (error instanceof ApiError && error.status === 404) {
    return (
      <MiddleColumn>
        <SeriesNotFound />
      </MiddleColumn>
    )
  }
  if (!series) {
    return (
      <MiddleColumn>
        <Hint error={!!error}>{error ? error.message : '加载中…'}</Hint>
      </MiddleColumn>
    )
  }

  const selection = resolveSelection(series, seasonId, episodeId)
  return (
    <>
      <SeriesColumn series={series} selection={selection} />
      <section className="min-h-0 overflow-y-auto">
        <RightPanel series={series} selection={selection} />
      </section>
    </>
  )
}

/** 右栏：集面板、季面板，或季、集已经不存在时的提示 */
function RightPanel({ series, selection }: { series: SeriesDetail; selection: Selection }) {
  const { season, episode } = selection
  if (selection.missing === 'season') {
    return <NotFound what="这一季" backTo={catalogPath(series.id)} backLabel={series.title} />
  }
  if (selection.missing === 'episode') {
    // 电影在界面上没有季，返回这部电影
    const movie = series.type === 'movie'
    return (
      <NotFound
        what="这一集"
        backTo={movie ? catalogPath(series.id) : catalogPath(series.id, selection.season.id)}
        backLabel={movie ? series.title : seasonName(selection.season)}
      />
    )
  }
  if (season && episode) {
    return <EpisodePanel series={series} season={season} episode={episode} />
  }
  return season ? <SeasonPanel series={series} season={season} /> : null
}

function MiddleColumn({ children }: { children: ReactNode }) {
  return <section className="min-h-0 overflow-y-auto border-r">{children}</section>
}

function SeriesNotFound() {
  return <NotFound what="这部剧" backTo={catalogPath()} backLabel="目录" />
}
