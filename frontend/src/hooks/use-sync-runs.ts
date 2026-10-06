import { useCallback, useEffect, useRef } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'

import { getLatestSyncRun, getSyncRun, listSyncRuns } from '@/api/sync'
import { useReloadSeries } from '@/hooks/use-series'

export const syncRunKeys = {
  list: ['sync-runs'] as const,
  detail: (id: number) => ['sync-runs', id] as const,
  latest: ['sync-runs-latest'] as const,
}

/** 轮询间隔：最近一次同步全局一直轮询，列表、详情只在同步页轮询 */
const pollInterval = 2000

/** 最近几次同步（后端配置 sync.keep_runs，默认 20），新的在前；只在同步页用，一直轮询 */
export function useSyncRuns() {
  return useQuery({
    queryKey: syncRunKeys.list,
    queryFn: listSyncRuns,
    refetchInterval: pollInterval,
  })
}

/** 一次同步的详情，含警告；还在运行时轮询，警告随进度增加 */
export function useSyncRun(id: number) {
  return useQuery({
    queryKey: syncRunKeys.detail(id),
    queryFn: () => getSyncRun(id),
    refetchInterval: (query) => (query.state.data?.status === 'running' ? pollInterval : false),
  })
}

/**
 * 返回一个函数，让同步列表和最近一次同步失效、重新加载。触发同步之后调用：
 * 返回时这次同步的记录已经建出，立即刷新，不等下一次轮询。
 * 详情的键以列表的键为前缀，列表只按 exact 失效，不连带已加载的详情（它们自己按状态轮询）
 */
export function useReloadSyncRuns() {
  const queryClient = useQueryClient()
  return useCallback(
    () =>
      Promise.all([
        queryClient.invalidateQueries({ queryKey: syncRunKeys.list, exact: true }),
        queryClient.invalidateQueries({ queryKey: syncRunKeys.latest }),
      ]),
    [queryClient],
  )
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
