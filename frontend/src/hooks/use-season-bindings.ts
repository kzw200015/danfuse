import { useCallback, useEffect, useRef } from 'react'
import { queryOptions, useQuery, useQueryClient } from '@tanstack/react-query'

import {
  getDefaultEpisodePatterns,
  getSeasonBinding,
  type SeasonBinding,
  type SeasonBindingDetail,
} from '@/api/season-bindings'
import { useReloadSeries } from '@/hooks/use-series'

/** 查询键：季绑定详情 ['season-bindings', id]。季绑定的列表在剧详情里，没有单独的查询 */
export const seasonBindingKeys = {
  detail: (id: number) => ['season-bindings', id] as const,
}

/** 默认的集号规则，写死在后端，取一次就不再刷新；查询键 ['episode-rules', 'default'] */
export const defaultEpisodePatternsOptions = queryOptions({
  queryKey: ['episode-rules', 'default'],
  queryFn: async () => (await getDefaultEpisodePatterns()).episodePatterns,
  staleTime: Infinity,
})

export function useDefaultEpisodePatterns() {
  return useQuery(defaultEpisodePatternsOptions)
}

/** 正在补建时轮询详情的间隔 */
const pollInterval = 1000

function seasonBindingOptions(id: number) {
  return queryOptions({
    queryKey: seasonBindingKeys.detail(id),
    queryFn: () => getSeasonBinding(id),
  })
}

/**
 * 一个季绑定的详情与补建进度。binding 是剧详情里的这个季绑定（不含条目表），summaryUpdatedAt 是剧详情取到的时间。
 * 详情在展开条目表、或正在补建时才取；正在补建时每秒轮询。返回的 view 是剧详情与详情里较新的那一份：
 * 补建进行中建出的绑定数等随轮询逐条更新；剧详情重新加载后显示正在补建（例如追更的定时检查开始了）时，重新取一次详情。
 * 补建结束（running 由 true 变为 false）后让剧详情失效，各集的绑定数随之更新。
 */
export function useSeasonBinding(
  binding: SeasonBinding,
  summaryUpdatedAt: number,
  expanded: boolean,
) {
  const reloadSeries = useReloadSeries()
  const query = useQuery({
    ...seasonBindingOptions(binding.id),
    enabled: (q) => expanded || binding.running || !!q.state.data?.running,
    refetchInterval: (q) => (q.state.data?.running ? pollInterval : false),
  })
  const { refetch } = query
  const view: SeasonBinding =
    query.data && query.dataUpdatedAt >= summaryUpdatedAt ? query.data : binding
  const { running } = view

  useEffect(() => {
    if (binding.running) void refetch()
  }, [binding.running, refetch])

  const wasRunning = useRef(running)
  useEffect(() => {
    if (wasRunning.current && !running) void reloadSeries()
    wasRunning.current = running
  }, [running, reloadSeries])

  return { view, detail: query.data, error: query.error }
}

/**
 * 返回一个函数：创建、立即补建、改集号对应、开关追更成功后调用，把季绑定的最新详情放进缓存（没给时重新取一次）。
 * 详情显示正在补建时，useSeasonBinding 随即开始轮询。
 */
export function useWatchSeasonBinding() {
  const queryClient = useQueryClient()
  return useCallback(
    async (id: number, detail?: SeasonBindingDetail) => {
      if (detail) {
        queryClient.setQueryData(seasonBindingKeys.detail(id), detail)
      } else {
        await queryClient.fetchQuery(seasonBindingOptions(id))
      }
    },
    [queryClient],
  )
}
