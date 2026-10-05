import {
  CircleCheckIcon,
  CircleSlashIcon,
  CircleXIcon,
  Loader2Icon,
  TriangleAlertIcon,
  type LucideIcon,
  type LucideProps,
} from 'lucide-react'

import type { SyncRun, SyncStatus } from '@/api/sync'
import { runStatusText } from '@/lib/sync'
import { cn } from '@/lib/utils'

const statusIcons: Record<SyncStatus, { Icon: LucideIcon; className: string }> = {
  running: { Icon: Loader2Icon, className: 'animate-spin text-sky-600' },
  succeeded: { Icon: CircleCheckIcon, className: 'text-emerald-600' },
  failed: { Icon: CircleXIcon, className: 'text-destructive' },
  interrupted: { Icon: CircleSlashIcon, className: 'text-amber-600' },
}

export function RunStatusIcon({
  status,
  className,
  ...props
}: LucideProps & { status: SyncStatus }) {
  const { Icon, className: statusClassName } = statusIcons[status]
  return <Icon className={cn('size-4 shrink-0', statusClassName, className)} {...props} />
}

/** "同步"导航项上的状态：进行中显示进度，失败、中断显示图标，有警告显示条数，正常结束不显示 */
export function SyncNavStatus({ run }: { run: SyncRun | undefined }) {
  if (!run) return null
  if (run.status === 'running') {
    return (
      <span
        className="inline-flex items-center gap-1 text-xs text-sky-700 tabular-nums"
        title={run.total === null ? '正在列出媒体库' : '正在同步'}
      >
        <RunStatusIcon status="running" className="size-3.5" />
        {run.total === null ? '…' : `${run.done}/${run.total}`}
      </span>
    )
  }
  if (run.status === 'failed' || run.status === 'interrupted') {
    const label = `最近一次同步${runStatusText[run.status]}`
    return (
      <RunStatusIcon status={run.status} className="size-3.5" role="img" aria-label={label}>
        <title>{label}</title>
      </RunStatusIcon>
    )
  }
  if (run.warningCount > 0) {
    return (
      <span
        className="inline-flex items-center gap-0.5 text-xs text-amber-700 tabular-nums"
        title={`最近一次同步有 ${run.warningCount} 条警告`}
      >
        <TriangleAlertIcon className="size-3" />
        {run.warningCount}
      </span>
    )
  }
  return null
}
