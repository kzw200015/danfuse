import { useState } from 'react'
import { LinkIcon, UploadIcon } from 'lucide-react'

import type { Binding } from '@/api/bindings'
import { episodeExportUrl } from '@/api/export'
import { deleteEpisode, type Episode, type Season, type SeriesDetail } from '@/api/series'
import { Button } from '@/components/ui/button'
import { formatDuration } from '@/lib/time'

import AddFileBindingForm from './AddFileBindingForm'
import AddLinkBindingForm from './AddLinkBindingForm'
import BindingCard from './BindingCard'
import { hasDanmaku, seasonName } from './catalog'
import DeleteButton from './DeleteButton'
import ExportButton from './ExportButton'
import { useCatalogPath } from './shared'

/**
 * 右栏：选中一集时的集面板。电影的唯一一集标题为"正片"，路径里不写季；电影只能整部删除，没有"删除这一集"。
 * 右上角"导出 XML"：导出这一集各绑定的弹幕（没有绑定保存了弹幕时禁用）；
 * "绑定链接""上传弹幕文件"在卡片列表上方展开添加的表单，同一时间只展开一个，和季面板一样
 */
export default function EpisodePanel({
  series,
  season,
  episode,
}: {
  series: SeriesDetail
  season: Season
  episode: Episode
}) {
  const path = useCatalogPath()
  const movie = series.type === 'movie'
  // 展开的添加表单，记着是哪一集的：换到别的集时自然收起，上一集的输入和失败提示不带过来
  const [adding, setAdding] = useState<{ episodeId: number; kind: Binding['kind'] } | null>(null)
  const open = adding?.episodeId === episode.id ? adding.kind : null
  const close = () => setAdding(null)
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

      <section aria-label="绑定" className="grid gap-3 border-t pt-4">
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-sm font-medium">绑定（{episode.bindings.length}）</h3>
          <div className="flex gap-1">
            <ExportButton
              href={episodeExportUrl(episode.id)}
              title="导出这一集各绑定的弹幕（zip，每个绑定一份 XML，按偏移校正）"
              disabled={!hasDanmaku(episode)}
            />
            <Button
              variant="outline"
              size="sm"
              disabled={open !== null}
              onClick={() => setAdding({ episodeId: episode.id, kind: 'link' })}
            >
              <LinkIcon />
              绑定链接
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={open !== null}
              onClick={() => setAdding({ episodeId: episode.id, kind: 'file' })}
            >
              <UploadIcon />
              上传弹幕文件
            </Button>
          </div>
        </div>
        {open === 'link' && <AddLinkBindingForm episodeId={episode.id} onClose={close} />}
        {open === 'file' && <AddFileBindingForm episodeId={episode.id} onClose={close} />}
        {episode.bindings.map((b) => (
          <BindingCard
            key={b.id}
            binding={b}
            episodeDuration={episode.duration}
            season={season}
            seriesId={series.id}
          />
        ))}
        {episode.bindings.length === 0 && (
          <p className="text-sm text-muted-foreground">
            还没有绑定。点右上角"绑定链接"贴一条 B 站链接，或"上传弹幕文件"，弹幕会立即保存。
          </p>
        )}
      </section>

      {!movie && (
        <div className="border-t pt-4">
          {/* 换一集时重新挂载，上一集删除失败的提示、进行中的删除不带过来 */}
          <DeleteButton
            key={episode.id}
            label="删除这一集"
            name={`第 ${episode.number} 集`}
            target={episode}
            remove={() => deleteEpisode(episode.id)}
            backTo={path(series.id, season.id)}
          />
        </div>
      )}
    </div>
  )
}
