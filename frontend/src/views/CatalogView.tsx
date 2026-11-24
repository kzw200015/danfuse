import type { ReactNode } from 'react'
import { useParams } from 'react-router'

import { isApiStatus } from '@/api/request'
import type { SeriesDetail } from '@/api/series'
import { useSeries } from '@/hooks/use-series'
import { parseId } from '@/lib/route'
import { cn } from '@/lib/utils'

import { resolveSelection, seasonName, type Selection } from './catalog/catalog'
import EpisodePanel from './catalog/EpisodePanel'
import SeasonPanel from './catalog/SeasonPanel'
import SeriesColumn from './catalog/SeriesColumn'
import SeriesList from './catalog/SeriesList'
import { DrillHeader, Hint, NotFound, useCatalogPath } from './catalog/shared'

/**
 * 目录页：剧列表 | 选中的剧 | 集面板或季面板，三栏占满顶栏以下的高度。
 * 响应式策略：< md 单栏下钻，md 双栏主从，>= lg 保持经典三栏；SeriesList 始终挂载保活筛选与搜索状态。
 * 选中的剧、季、集都在地址栏里：/catalog[/:seriesId[/:seasonId[/:episodeId | /season]]]
 */
export default function CatalogView() {
  const { seriesId, seasonId, episodeId } = useParams()
  const id = seriesId === undefined ? undefined : parseId(seriesId)
  const path = useCatalogPath()

  return (
    <div className="grid h-full grid-cols-1 grid-rows-1 md:grid-cols-[16rem_1fr] lg:grid-cols-[17rem_21rem_1fr]">
      <SeriesList selectedId={id} className={cn(seriesId !== undefined && 'hidden md:flex')} />
      {seriesId === undefined ? (
        <MiddleColumn className="hidden md:block">
          <Hint>选择一部剧</Hint>
        </MiddleColumn>
      ) : id === undefined ? (
        <MiddleColumn>
          <DrillHeader backTo={path()} backLabel="目录" className="md:hidden" />
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
  const path = useCatalogPath()
  const { data: series, error } = useSeries(id)

  if (isApiStatus(error, 404)) {
    return (
      <MiddleColumn>
        <DrillHeader backTo={path()} backLabel="目录" className="md:hidden" />
        <SeriesNotFound />
      </MiddleColumn>
    )
  }
  if (!series) {
    return (
      <MiddleColumn>
        <DrillHeader backTo={path()} backLabel="目录" className="md:hidden" />
        <Hint error={!!error}>{error ? error.message : '加载中…'}</Hint>
      </MiddleColumn>
    )
  }

  const selection = resolveSelection(series, seasonId, episodeId)
  // 手机端 (< md) 与平板双栏 (md) 下，只有显式进入 /season、选中单集、电影直达正片或错误页时才切入右栏
  const rightPanelActive = Boolean(selection.missing || selection.episode || episodeId === 'season')
  return (
    <>
      <SeriesColumn
        series={series}
        selection={selection}
        className={cn(rightPanelActive && 'hidden lg:flex')}
      />
      <section className={cn('min-h-0 overflow-y-auto', !rightPanelActive && 'hidden lg:block')}>
        <RightPanel series={series} selection={selection} />
      </section>
    </>
  )
}

/** 右栏：集面板、季面板，或季、集已经不存在时的提示 */
function RightPanel({ series, selection }: { series: SeriesDetail; selection: Selection }) {
  const path = useCatalogPath()
  const { season, episode } = selection
  if (selection.missing === 'season') {
    return <NotFound what="这一季" backTo={path(series.id)} backLabel={series.title} />
  }
  if (selection.missing === 'episode') {
    // 电影在界面上没有季，返回这部电影
    const movie = series.type === 'movie'
    return (
      <NotFound
        what="这一集"
        backTo={movie ? path(series.id) : path(series.id, selection.season.id)}
        backLabel={movie ? series.title : seasonName(selection.season)}
      />
    )
  }
  if (season && episode) {
    return <EpisodePanel series={series} season={season} episode={episode} />
  }
  return season ? <SeasonPanel series={series} season={season} /> : null
}

function MiddleColumn({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <section className={cn('min-h-0 overflow-y-auto md:border-r', className)}>{children}</section>
  )
}

function SeriesNotFound() {
  const path = useCatalogPath()
  return <NotFound what="这部剧" backTo={path()} backLabel="目录" />
}
