import { deleteSeason, type Season, type SeriesDetail } from '@/api/series'

import { bindingStats, catalogPath, seasonName } from './catalog'
import DeleteButton from './DeleteButton'

/** 右栏：选中整季、没选集时的季面板 */
export default function SeasonPanel({ series, season }: { series: SeriesDetail; season: Season }) {
  const stats = bindingStats(season.episodes)
  return (
    <div className="mx-auto grid max-w-3xl gap-4 p-5">
      <div>
        <div className="text-xs text-muted-foreground">{series.title}</div>
        <h2 className="text-lg font-semibold">
          {seasonName(season)}
          {season.title && <span className="ml-2 font-normal">{season.title}</span>}
        </h2>
        <div className="text-xs text-muted-foreground">
          {season.episodes.length} 集 · 已绑定 {stats.bound} 集
          {stats.dead > 0 && <span className="text-destructive"> · {stats.dead} 个失效绑定</span>} ·
          季 ID {season.id}
        </div>
      </div>
      <DeleteButton
        key={season.id}
        label="删除这一季"
        name={seasonName(season)}
        target={season}
        remove={() => deleteSeason(season.id)}
        backTo={catalogPath(series.id)}
      />
    </div>
  )
}
