import { Fragment } from 'react'
import { TriangleAlertIcon } from 'lucide-react'
import { Link } from 'react-router'

import { deleteSeries, type Episode, type SeriesDetail } from '@/api/series'
import { Badge } from '@/components/ui/badge'
import { buttonVariants } from '@/components/ui/button'
import { formatDuration } from '@/lib/time'
import { cn } from '@/lib/utils'

import {
  bilibiliSearchUrl,
  seasonLabel,
  seasonName,
  seriesDetailMeta,
  tmdbUrl,
  type Selection,
} from './catalog'
import DeleteButton from './DeleteButton'
import { DrillHeader, Hint, Poster, scrollIntoView, SourceLink, useCatalogPath } from './shared'

/** 中栏：选中的剧，以及它的季切换、季标题行和集列表。电影只有一集，不显示季切换和集列表 */
export default function SeriesColumn({
  series,
  selection: { season, episode, missing },
  className,
}: {
  series: SeriesDetail
  selection: Selection
  className?: string
}) {
  const path = useCatalogPath()
  const tmdb = tmdbUrl(series)
  const links = [
    // 核对 Jellyfin 识别得对不对；同步按 TMDB ID 对应这部剧
    tmdb && { href: tmdb, label: 'TMDB' },
    // 回到目录源里这部剧的页面
    series.sourceLink && { href: series.sourceLink.url, label: series.sourceLink.label },
    // 找弹幕源：用标题在 B 站综合搜索
    { href: bilibiliSearchUrl(series.title), label: 'B 站' },
  ].filter((link) => !!link)
  const seasonPanelOpen = !episode && !missing
  return (
    <section className={cn('flex min-h-0 flex-col lg:border-r', className)}>
      <DrillHeader backTo={path()} backLabel="目录" title={series.title} className="md:hidden" />
      <div className="flex gap-3 border-b p-3">
        <Poster imageId={series.posterImageId} className="w-16" />
        <div className="min-w-0 flex-1">
          <h2 className="font-semibold">{series.title}</h2>
          {series.originalTitle && (
            <div className="truncate text-xs text-muted-foreground">{series.originalTitle}</div>
          )}
          <div className="text-xs text-muted-foreground">{seriesDetailMeta(series)}</div>
          {series.tmdbId === null && (
            <div className="mt-1.5 flex items-start gap-1 rounded-md bg-amber-500/10 px-2 py-1 text-xs text-amber-700 md:hidden">
              <TriangleAlertIcon className="mt-0.5 size-3 shrink-0" />
              <span>无 TMDB ID：媒体库改标题或年份会新建剧</span>
            </div>
          )}
          {/* 外链单独一行；手机端做成隔离胶囊并加大与下方删除按钮间距，避免误触 */}
          <div className="mt-1.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground md:mt-0 md:gap-1">
            {links.map(({ href, label }, i) => (
              <Fragment key={label}>
                {i > 0 && <span className="hidden md:inline">·</span>}
                <SourceLink
                  href={href}
                  className="rounded-md bg-muted/60 px-2 py-1 font-normal break-normal whitespace-nowrap md:rounded-none md:bg-transparent md:p-0"
                >
                  {label}
                </SourceLink>
              </Fragment>
            ))}
          </div>
          <DeleteButton
            key={series.id}
            className="mt-2.5 -ml-2.5 md:mt-1"
            label="删除这部剧"
            name={`「${series.title}」`}
            target={series}
            remove={() => deleteSeries(series.id)}
            backTo={path()}
          />
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
                  to={path(series.id, se.id)}
                  aria-current={selected ? 'true' : undefined}
                  className={buttonVariants({
                    size: 'xs',
                    variant: selected ? 'secondary' : 'ghost',
                    className: 'min-h-9 px-3 md:min-h-0 md:px-2',
                  })}
                >
                  {seasonLabel(se)}
                </Link>
              )
            })}
          </nav>
          {season && (
            <>
              {/* 季标题行：打开季面板；显式指向 /season 以在手机/平板区分切季与进入季面板 */}
              <Link
                to={path(series.id, season.id, 'season')}
                title="查看整季"
                aria-current={seasonPanelOpen ? 'true' : undefined}
                className={cn(
                  'mx-2 my-1.5 flex min-h-11 items-center gap-2 rounded-lg border bg-muted/40 px-3 py-2 text-sm hover:bg-muted/60 md:mx-0 md:my-0 md:min-h-0 md:rounded-none md:border-0 md:bg-transparent md:py-2 md:text-xs',
                  seasonPanelOpen && 'bg-muted md:bg-muted',
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
                        to={path(series.id, season.id, e.id)}
                        ref={selected ? scrollIntoView : undefined}
                        aria-current={selected ? 'true' : undefined}
                        className={cn(
                          'flex min-h-11 items-center gap-2 px-3 py-2 text-sm hover:bg-muted/60 md:min-h-0 md:py-1.5',
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
                        <BindingCount episode={e} />
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

/** 集列表里一集的绑定数，有失效的绑定时标红；没有绑定时留空 */
function BindingCount({ episode }: { episode: Episode }) {
  const total = episode.bindings.length
  const dead = episode.bindings.filter((b) => b.status === 'dead').length
  return (
    <span className="flex w-6 shrink-0 justify-end">
      {total > 0 && (
        <Badge
          variant={dead > 0 ? 'destructive' : 'secondary'}
          className="h-4 px-1.5 text-[10px] tabular-nums"
          title={dead > 0 ? `${total} 个绑定，${dead} 个失效` : `${total} 个绑定`}
        >
          {total}
        </Badge>
      )}
    </span>
  )
}
