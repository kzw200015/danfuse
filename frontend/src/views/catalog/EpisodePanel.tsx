import type { Episode, Season, SeriesDetail } from '@/api/series'

import { formatDuration, seasonName } from './catalog'

/** 右栏：选中一集时的集面板。电影的唯一一集标题为"正片"，路径里不写季 */
export default function EpisodePanel({
  series,
  season,
  episode,
}: {
  series: SeriesDetail
  season: Season
  episode: Episode
}) {
  const movie = series.type === 'movie'
  return (
    <div className="mx-auto grid max-w-3xl gap-4 p-5">
      <div>
        <div className="text-xs text-muted-foreground">
          {series.title}
          {!movie && ` › ${seasonName(season)}`}
        </div>
        <h2 className="text-lg font-semibold">
          {movie ? '正片' : `第 ${episode.number} 集`}
          {episode.title && <span className="ml-2 font-normal">{episode.title}</span>}
        </h2>
        <div className="text-xs text-muted-foreground">
          时长 {formatDuration(episode.duration)} · 集 ID {episode.id}
        </div>
      </div>
    </div>
  )
}
