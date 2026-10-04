import type { Episode, Season, SeriesDetail } from '@/api/series'

import AddBindingForm from './AddBindingForm'
import BindingCard from './BindingCard'
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

      {/* 换一集时重新挂载，上一集的输入和失败提示不带过来 */}
      <AddBindingForm key={episode.id} episodeId={episode.id} />

      <section aria-label="绑定" className="grid gap-3 border-t pt-4">
        <h3 className="text-sm font-medium">绑定（{episode.bindings.length}）</h3>
        {episode.bindings.map((b) => (
          <BindingCard key={b.id} binding={b} episodeDuration={episode.duration} />
        ))}
        {episode.bindings.length === 0 && (
          <p className="text-sm text-muted-foreground">
            还没有绑定。粘贴一条 B 站链接，弹幕会立即拉取保存。
          </p>
        )}
      </section>
    </div>
  )
}
