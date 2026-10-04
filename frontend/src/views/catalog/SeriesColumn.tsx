import { Link } from 'react-router'

import type { SeriesDetail } from '@/api/series'
import { buttonVariants } from '@/components/ui/button'
import { cn } from '@/lib/utils'

import {
  catalogPath,
  formatDuration,
  seasonLabel,
  seasonName,
  seriesMeta,
  type Selection,
} from './catalog'
import { Hint, Poster, scrollIntoView } from './shared'

/** 中栏：选中的剧，以及它的季切换、季标题行和集列表。电影只有一集，不显示季切换和集列表 */
export default function SeriesColumn({
  series,
  selection: { season, episode, missing },
}: {
  series: SeriesDetail
  selection: Selection
}) {
  const seasonPanelOpen = !episode && !missing
  return (
    <section className="flex min-h-0 flex-col border-r">
      <div className="flex gap-3 border-b p-3">
        <Poster className="w-16" />
        <div className="min-w-0 flex-1">
          <h2 className="font-semibold">{series.title}</h2>
          {series.originalTitle && (
            <div className="truncate text-xs text-muted-foreground">{series.originalTitle}</div>
          )}
          <div className="text-xs text-muted-foreground">{seriesMeta(series)}</div>
        </div>
      </div>

      {series.type === 'movie' ? (
        <Hint>电影只有正片一集，见右侧。</Hint>
      ) : series.seasons.length === 0 ? (
        <Hint>这部剧没有季</Hint>
      ) : (
        <>
          <nav aria-label="季" className="flex flex-wrap gap-1 border-b p-2">
            {series.seasons.map((se) => {
              const selected = se.id === season?.id
              return (
                <Link
                  key={se.id}
                  to={catalogPath(series.id, se.id)}
                  aria-current={selected ? 'true' : undefined}
                  className={buttonVariants({
                    size: 'xs',
                    variant: selected ? 'secondary' : 'ghost',
                  })}
                >
                  {seasonLabel(se)}
                </Link>
              )
            })}
          </nav>
          {season && (
            <>
              {/* 季标题行：在右栏打开季面板 */}
              <Link
                to={catalogPath(series.id, season.id)}
                title="查看整季"
                aria-current={seasonPanelOpen ? 'true' : undefined}
                className={cn(
                  'flex items-center gap-2 px-3 py-2 text-xs hover:bg-muted/60',
                  seasonPanelOpen && 'bg-muted',
                )}
              >
                <span className="font-medium">{seasonName(season)}</span>
                {season.title && (
                  <span className="truncate text-muted-foreground">{season.title}</span>
                )}
                <span className="shrink-0 text-muted-foreground">
                  · {season.episodes.length} 集
                </span>
                <span className="ml-auto shrink-0 text-muted-foreground">整季 ›</span>
              </Link>
              <ul className="min-h-0 flex-1 overflow-y-auto">
                {season.episodes.map((e) => {
                  const selected = e.id === episode?.id
                  return (
                    <li key={e.id}>
                      <Link
                        to={catalogPath(series.id, season.id, e.id)}
                        ref={selected ? scrollIntoView : undefined}
                        aria-current={selected ? 'true' : undefined}
                        className={cn(
                          'flex items-center gap-2 px-3 py-1.5 text-sm hover:bg-muted/60',
                          selected && 'bg-muted',
                        )}
                      >
                        <span className="w-8 shrink-0 text-right text-xs text-muted-foreground tabular-nums">
                          {e.number}
                        </span>
                        <span className="min-w-0 flex-1 truncate">{e.title ?? '—'}</span>
                        <span className="text-xs text-muted-foreground tabular-nums">
                          {formatDuration(e.duration)}
                        </span>
                      </Link>
                    </li>
                  )
                })}
              </ul>
            </>
          )}
        </>
      )}
    </section>
  )
}
