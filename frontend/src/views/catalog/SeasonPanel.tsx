import { deleteSeason, type Season, type SeriesDetail } from '@/api/series'
import { useSeries } from '@/hooks/use-series'

import AddSeasonBindingForm from './AddSeasonBindingForm'
import { bindingStats, seasonName } from './catalog'
import DeleteButton from './DeleteButton'
import SeasonBindingCard from './SeasonBindingCard'
import { useCatalogPath } from './shared'

/** 右栏：选中整季、没选集时的季面板：这一季的信息、季绑定，以及"删除这一季" */
export default function SeasonPanel({ series, season }: { series: SeriesDetail; season: Season }) {
  const path = useCatalogPath()
  const stats = bindingStats(season.episodes)
  // 与剧详情同一份缓存，不另外请求；季绑定卡片用它判断剧详情和轮询到的详情哪个新
  const { dataUpdatedAt } = useSeries(series.id)
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

      <section aria-label="季绑定" className="grid gap-3 border-t pt-4">
        <h3 className="text-sm font-medium">季绑定（{season.seasonBindings.length}）</h3>
        <p className="text-xs text-muted-foreground">
          把 B 站番剧的一季、投稿合集或多 P
          投稿绑到这一季上，按集号对应在后台为各集建出绑定；开着追更时自动补建新出的集。
        </p>
        {/* 换一季时重新挂载，上一季的输入和预览不带过来 */}
        <AddSeasonBindingForm key={season.id} season={season} />
        {season.seasonBindings.map((sb) => (
          <SeasonBindingCard key={sb.id} binding={sb} summaryUpdatedAt={dataUpdatedAt} />
        ))}
      </section>

      <div className="border-t pt-4">
        <DeleteButton
          key={season.id}
          label="删除这一季"
          name={seasonName(season)}
          target={season}
          remove={() => deleteSeason(season.id)}
          backTo={path(series.id)}
        />
      </div>
    </div>
  )
}
