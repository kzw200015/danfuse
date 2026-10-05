import type { Settings } from '@/api/settings'
import type { SyncRun, SyncStatus, SyncTrigger } from '@/api/sync'
import { formatSeconds } from '@/lib/time'

export const runStatusText: Record<SyncStatus, string> = {
  running: '同步中',
  succeeded: '成功',
  failed: '失败',
  interrupted: '已中断',
}

export const triggerText: Record<SyncTrigger, string> = { manual: '手动', schedule: '定时' }

/** 已完成/总数；列完媒体库之前总数未知 */
export function runProgressText(run: SyncRun) {
  if (run.total !== null) return `${run.done} / ${run.total} 部`
  return run.status === 'running' ? '正在列出媒体库…' : '—'
}

/** 进度条的值（百分比）；还在列出媒体库时总数未知，为 null 表示进度不确定 */
export function progressValue(run: SyncRun) {
  if (run.total === null) return run.status === 'running' ? null : 0
  if (run.total === 0) return run.status === 'succeeded' ? 100 : 0
  return (run.done / run.total) * 100
}

/** 同步页标题旁的定时同步说明 */
export function scheduleText(settings: Settings) {
  if (settings.catalogSource === null) return '未配置目录源'
  if (settings.syncInterval === 0) return '未开启定时同步'
  return `每 ${formatSeconds(settings.syncInterval)}自动同步一次`
}
