import { request } from './request'

export type SyncTrigger = 'manual' | 'schedule'

export type SyncStatus = 'running' | 'succeeded' | 'failed' | 'interrupted'

/** 同步记录（列表项），对应后端 GET /api/sync-runs */
export interface SyncRun {
  id: number
  trigger: SyncTrigger
  /** 判断同步是否在运行只看 status：残留后被改为 interrupted 的记录 finishedAt 也为空 */
  status: SyncStatus
  startedAt: string
  finishedAt: string | null
  /** 剧（含电影）的总数，列完媒体库之前为 null */
  total: number | null
  done: number
  createdSeries: number
  createdSeasons: number
  createdEpisodes: number
  warningCount: number
  /** 失败原因 */
  error: string | null
}

/** 一次同步的详情，比列表项多警告正文（最多保存 200 条，总数见 warningCount） */
export interface SyncRunDetail extends SyncRun {
  warnings: string[]
}

/** 最近 20 次同步，新的在前 */
export function listSyncRuns() {
  return request<SyncRun[]>({ url: '/sync-runs' })
}

export function getSyncRun(id: number) {
  return request<SyncRunDetail>({ url: `/sync-runs/${id}` })
}

/** 最近一次同步，与列表项相同；一次都没同步过时为 null */
export function getLatestSyncRun() {
  return request<SyncRun | null>({ url: '/sync-runs/latest' })
}

/** 触发一次同步，不等它开始；已有同步在跑时这次触发被丢弃，同样成功。未配置目录源时为 409 */
export function triggerSync() {
  return request<null>({ url: '/sync-runs', method: 'POST' })
}
