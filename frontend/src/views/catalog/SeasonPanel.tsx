import { useState } from 'react'
import { FolderUpIcon, PlusIcon } from 'lucide-react'

import { seasonExportUrl } from '@/api/export'
import type { SeasonBindingKind } from '@/api/season-bindings'
import { deleteSeason, type Season, type SeriesDetail } from '@/api/series'
import { Button } from '@/components/ui/button'
import { useSeries } from '@/hooks/use-series'

import { bindingStats, hasDanmaku, seasonName } from './catalog'
import { AddCollectionForm } from './CollectionForm'
import DeleteButton from './DeleteButton'
import ExportButton from './ExportButton'
import { AddFolderForm } from './FolderForm'
import SeasonBindingCard from './SeasonBindingCard'
import { useCatalogPath } from './shared'

/**
 * 右栏：选中整季、没选集时的季面板：这一季的信息、季绑定，以及"删除这一季"。
 * 右上角"导出 XML"下载整季各集的弹幕（整季都没有绑定保存了弹幕时禁用；电影没有季面板，也就没有季导出）；
 * "绑定合集""上传弹幕文件夹"在卡片列表上方展开添加的表单，同一时间只展开一个
 */
export default function SeasonPanel({ series, season }: { series: SeriesDetail; season: Season }) {
  const path = useCatalogPath()
  const stats = bindingStats(season.episodes)
  // 与剧详情同一份缓存，不另外请求；季绑定卡片用它判断剧详情和轮询到的详情哪个新
  const { dataUpdatedAt } = useSeries(series.id)
  // 展开的添加表单，记着是哪一季的：换到别的季时自然收起
  const [adding, setAdding] = useState<{ seasonId: number; kind: SeasonBindingKind } | null>(null)
  const open = adding?.seasonId === season.id ? adding.kind : null
  const close = () => setAdding(null)
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
        <div className="flex items-center justify-between gap-2">
          <h3 className="text-sm font-medium">季绑定（{season.seasonBindings.length}）</h3>
          <div className="flex gap-1">
            <ExportButton
              href={seasonExportUrl(season.id)}
              title="下载这一季各集的弹幕（zip，每集一个子目录、每个绑定一份 XML，按偏移校正）"
              disabled={!season.episodes.some(hasDanmaku)}
            />
            <Button
              variant="outline"
              size="sm"
              disabled={open !== null}
              onClick={() => setAdding({ seasonId: season.id, kind: 'collection' })}
            >
              <PlusIcon />
              绑定合集
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={open !== null}
              onClick={() => setAdding({ seasonId: season.id, kind: 'folder' })}
            >
              <FolderUpIcon />
              上传弹幕文件夹
            </Button>
          </div>
        </div>
        <p className="text-xs text-muted-foreground">
          把 B 站番剧的一季、投稿合集或多 P
          投稿绑到这一季上，按集号对应在后台为各集建出绑定，开着追更时自动补建新出的集；手上有整季的弹幕存档时上传弹幕文件夹。
          季绑定建出的绑定只能在这里改。
        </p>
        {open === 'collection' && <AddCollectionForm season={season} onClose={close} />}
        {open === 'folder' && <AddFolderForm season={season} onClose={close} />}
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
