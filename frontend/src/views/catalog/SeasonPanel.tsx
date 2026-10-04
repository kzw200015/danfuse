import type { Season, SeriesDetail } from '@/api/series'

import { seasonName } from './catalog'

/** 右栏：选中整季、没选集时的季面板 */
export default function SeasonPanel({ series, season }: { series: SeriesDetail; season: Season }) {
  return (
    <div className="mx-auto grid max-w-3xl gap-4 p-5">
      <div>
        <div className="text-xs text-muted-foreground">{series.title}</div>
        <h2 className="text-lg font-semibold">
          {seasonName(season)}
          {season.title && <span className="ml-2 font-normal">{season.title}</span>}
        </h2>
        <div className="text-xs text-muted-foreground">
          {season.episodes.length} 集 · 季 ID {season.id}
        </div>
      </div>
    </div>
  )
}
