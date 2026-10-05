import { useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'

import { getLatestSyncRun, getSyncRun, listSyncRuns } from '@/api/sync'
import { useReloadSeries } from '@/hooks/use-series'

export const syncRunKeys = {
  list: ['sync-runs'] as const,
  detail: (id: number) => ['sync-runs', id] as const,
  latest: ['sync-runs-latest'] as const,
}

/** 轮询间隔：最近一次同步全局一直轮询，列表、详情只在同步页轮询 */
const pollInterval = 2000

/** 最近 20 次同步，新的在前；只在同步页用，一直轮询 */
export function useSyncRuns() {
  return useQuery({
    queryKey: syncRunKeys.list,
    queryFn: listSyncRuns,
    refetchInterval: pollInterval,
  })
}

/** 一次同步的详情，含警告；还在运行时轮询，警告随进度增加 */
export function useSyncRun(id: number | undefined) {
  return useQuery({
    queryKey: syncRunKeys.detail(id ?? 0),
    queryFn: () => getSyncRun(id!),
    enabled: id !== undefined,
    refetchInterval: (query) => (query.state.data?.status === 'running' ? pollInterval : false),
  })
}

/**
 * 最近一次同步，用于顶栏的同步状态：一直轮询，看到更新的一次同步结束后让剧列表和打开的剧的查询失效。
 * 只在根布局调用一次。
 */
export function useLatestSyncRun() {
  const reloadSeries = useReloadSeries()
  const { data: latest } = useQuery({
    queryKey: syncRunKeys.latest,
    queryFn: getLatestSyncRun,
    refetchInterval: pollInterval,
  })

  // 同步 ID 单调递增，handled 是已经处理过结束的同步里最新那次的 ID。第一次取到时之前的同步都算处理过；
  // 之后看到更新的一次同步结束（包括触发后很快就结束、没赶上轮询的），目录可能变了
  const handled = useRef<number>(undefined)
  useEffect(() => {
    if (latest === undefined) return
    if (handled.current === undefined) {
      handled.current = latest?.status === 'running' ? latest.id - 1 : (latest?.id ?? 0)
    } else if (latest && latest.status !== 'running' && latest.id > handled.current) {
      handled.current = latest.id
      void reloadSeries()
    }
  }, [latest, reloadSeries])

  return latest ?? undefined
}
