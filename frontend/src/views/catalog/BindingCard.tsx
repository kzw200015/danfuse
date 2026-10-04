import { ExternalLinkIcon } from 'lucide-react'

import type { Binding } from '@/api/bindings'
import { Badge } from '@/components/ui/badge'
import { formatAgo, formatDateTime } from '@/lib/time'
import { cn } from '@/lib/utils'

import { durationMismatch, formatDuration } from './catalog'

/** 一个绑定的卡片：状态、弹幕源标题（链接到来源）、来源标签、弹幕条数、上次拉取时间、与本集时长的对比 */
export default function BindingCard({
  binding,
  episodeDuration,
}: {
  binding: Binding
  /** 本集的时长，秒 */
  episodeDuration: number | null
}) {
  const dead = binding.status === 'dead'
  return (
    <article
      aria-label={binding.title}
      className={cn('grid gap-2 rounded-lg border p-3', dead && 'border-destructive/40')}
    >
      <div className="flex items-start gap-2">
        {dead ? (
          <Badge variant="destructive" title="上次拉取时弹幕源已不存在；已保存的弹幕仍照常输出">
            失效
          </Badge>
        ) : (
          <Badge variant="outline" className="border-emerald-600/30 text-emerald-700">
            正常
          </Badge>
        )}
        <div className="min-w-0 flex-1">
          <a
            href={binding.sourceUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 font-medium break-all hover:underline"
          >
            {binding.title}
            <ExternalLinkIcon className="size-3 shrink-0 opacity-50" />
          </a>
          <div className="text-xs text-muted-foreground">{binding.sourceLabel}</div>
        </div>
      </div>
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
        <span>弹幕 {binding.danmakuCount.toLocaleString()} 条</span>
        {binding.lastFetchedAt && (
          <span title={formatDateTime(binding.lastFetchedAt)}>
            上次拉取 {formatAgo(binding.lastFetchedAt)}
          </span>
        )}
        <DurationCompare source={binding.duration} episode={episodeDuration} />
      </div>
    </article>
  )
}

/** 弹幕源时长与本集时长的对比，相差 3 秒以上时标出 */
function DurationCompare({ source, episode }: { source: number; episode: number | null }) {
  const diff = durationMismatch(source, episode)
  return (
    <span className="tabular-nums">
      弹幕源 {formatDuration(source)} / 本集 {formatDuration(episode)}
      {diff !== null && (
        <span className="text-amber-600">
          （弹幕源{diff > 0 ? '长' : '短'} {Math.abs(diff)} 秒）
        </span>
      )}
    </span>
  )
}
