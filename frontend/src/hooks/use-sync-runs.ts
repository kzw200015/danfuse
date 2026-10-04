import { useEffect, useRef } from 'react'
import { queryOptions, useQuery, useQueryClient } from '@tanstack/react-query'

import { getSyncRun, listSyncRuns, type SyncRun } from '@/api/sync'
import { seriesKeys } from '@/hooks/use-series'

export const syncRunKeys = {
  list: ['sync-runs'] as const,
  detail: (id: number) => ['sync-runs', id] as const,
}

/** 同步进行中轮询详情的间隔 */
const pollInterval = 1000

/** 最近 20 次同步，新的在前 */
export function useSyncRuns() {
  return useQuery({ queryKey: syncRunKeys.list, queryFn: listSyncRuns })
}

/**
 * 一次同步的详情。取到后顺带替换同步列表里的这一条：进度只轮询详情，同步页的表格和顶栏的状态跟着列表变；
 * 列表里这一条不再是 running 时，轮询随之停止。
 */
function syncRunOptions(id: number) {
  return queryOptions({
    queryKey: syncRunKeys.detail(id),
    queryFn: async ({ client }) => {
      const run = await getSyncRun(id)
      client.setQueryData<SyncRun[]>(syncRunKeys.list, (runs) =>
        runs?.map((r) => (r.id === id ? run : r)),
      )
      return run
    },
  })
}

export function useSyncRun(id: number | undefined) {
  return useQuery({ ...syncRunOptions(id ?? 0), enabled: id !== undefined })
}

/**
 * 最近一次同步，用于顶栏的同步状态：它在运行时每秒轮询详情，结束后让剧列表和打开的剧的查询失效。
 * 只在根布局调用一次，同步页读同一份查询缓存，不另外轮询；没有同步在跑时不轮询。
 */
export function useLatestSyncRun() {
  const queryClient = useQueryClient()
  const { data: runs } = useSyncRuns()
  const latest = runs?.[0]

  useQuery({
    ...syncRunOptions(latest?.id ?? 0),
    enabled: latest?.status === 'running',
    refetchInterval: pollInterval,
  })

  // 同步 ID 单调递增，handled 是已经处理过结束的同步里最新那次的 ID。第一次取到列表时之前的同步都算处理过；
  // 之后看到更新的一次同步结束（包括触发后很快就结束、没赶上轮询的），目录可能变了
  const handled = useRef<number>(undefined)
  useEffect(() => {
    if (!runs) return
    if (handled.current === undefined) {
      handled.current = latest?.status === 'running' ? latest.id - 1 : (latest?.id ?? 0)
    } else if (latest && latest.status !== 'running' && latest.id > handled.current) {
      handled.current = latest.id
      // 剧详情的键以剧列表的键为前缀，一起失效
      void queryClient.invalidateQueries({ queryKey: seriesKeys.list })
    }
  }, [runs, latest, queryClient])

  return latest
}
