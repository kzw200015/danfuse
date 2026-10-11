import { useCallback, useEffect, useRef, useState } from 'react'
import {
  hashKey,
  keepPreviousData,
  queryOptions,
  useQuery,
  useQueryClient,
  type QueryKey,
} from '@tanstack/react-query'

import {
  getDefaultEpisodePatterns,
  getSeasonBinding,
  legacyGetSeasonBinding,
  type CollectionSeasonBinding,
  type LegacySeasonBindingDetail,
  type Preview,
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

/** 合集正在补建（文件夹的写入当场完成，没有补建中） */
function isRunning(sb: SeasonBinding | undefined) {
  return sb?.kind === 'collection' && sb.running
}

/**
 * 一个季绑定（两种都用）的详情与补建进度。binding 是剧详情里的这个季绑定（不含条目），summaryUpdatedAt 是剧详情取到的时间。
 * 详情在展开条目表时才取；合集正在补建时也取，并每秒轮询。返回的 view 是剧详情与详情里较新的那一份：
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
    queryKey: seasonBindingKeys.detail(binding.id),
    queryFn: () => getSeasonBinding(binding.id),
    enabled: (q) => expanded || isRunning(binding) || isRunning(q.state.data),
    refetchInterval: (q) => (isRunning(q.state.data) ? pollInterval : false),
  })
  const { refetch } = query
  const latest = query.data && query.dataUpdatedAt >= summaryUpdatedAt ? query.data : null
  const view: SeasonBinding = latest ?? binding
  const running = isRunning(view)
  const summaryRunning = isRunning(binding)

  useEffect(() => {
    if (summaryRunning) void refetch()
  }, [summaryRunning, refetch])

  const wasRunning = useRef(running)
  useEffect(() => {
    if (wasRunning.current && !running) void reloadSeries()
    wasRunning.current = running
  }, [running, reloadSeries])

  return { view, detail: query.data, error: query.error }
}

/**
 * 返回一个函数：季绑定的写入（创建、保存表单、开关追更、立即补建）成功后，把返回的详情放进缓存。
 * 合集的详情显示正在补建时，useSeasonBinding 随即开始轮询，补建结束后重新加载剧详情；
 * 文件夹的写入当场完成，随即重新加载剧详情（各集的绑定、季绑定的列表）。
 */
export function useWatchSeasonBinding() {
  const queryClient = useQueryClient()
  const reloadSeries = useReloadSeries()
  return useCallback(
    async (detail: SeasonBindingDetail) => {
      queryClient.setQueryData(seasonBindingKeys.detail(detail.id), detail)
      if (detail.kind === 'folder') await reloadSeries()
    },
    [queryClient, reloadSeries],
  )
}

/** 改了表单之后等这么久再重新预览（防抖） */
export const previewDebounce = 600

/**
 * value 的内容停止变化 delay 毫秒后才跟上它。按内容（查询键的哈希）比较：
 * 表单每次渲染都拼出新的对象，内容没变时不重新计时
 */
function useDebounced<T>(value: T, delay: number) {
  const key = hashKey([value])
  const [stable, setStable] = useState({ key, value })
  if (stable.key !== key) setStable({ key, value })
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(stable.value), delay)
    return () => clearTimeout(timer)
  }, [stable.value, delay])
  return debounced
}

/**
 * 季绑定表单的预览，随表单自动刷新：input 停止变化约 0.6 秒后，以 [...keyPrefix, input] 为查询键调用 preview。
 * input 为 null 时（表单还不完整，例如没贴链接、集号对应不合法）不预览；input 要能序列化成 JSON（比较时按内容）。
 * 预览不缓存（离开后即丢弃，每次都重新请求）；重新预览进行中时保留上一次的结果。
 * current 表示 preview 是按当前的 input 算出的：表单改了还没防抖完、正在重新预览、预览失败时都为 false，这时不能保存。
 */
export function useSeasonBindingPreview<T>(
  keyPrefix: QueryKey,
  preview: (input: T) => Promise<Preview>,
  input: T | null,
) {
  const debounced = useDebounced(input, previewDebounce)
  const query = useQuery({
    queryKey: [...keyPrefix, debounced],
    queryFn: () => preview(debounced!),
    enabled: debounced !== null,
    gcTime: 0,
    staleTime: Infinity,
    placeholderData: keepPreviousData,
  })
  const settled =
    input !== null &&
    hashKey([input]) === hashKey([debounced]) &&
    query.isSuccess &&
    !query.isPlaceholderData
  return {
    preview: query.data,
    current: settled,
    fetching: query.isFetching,
    error: query.error,
  }
}

// ---- 旧 hooks：只供还没换掉的旧组件（SeasonBindingCard、SeasonBindingForm）用，工单 14 换掉组件时删除 ----

function legacySeasonBindingOptions(id: number) {
  return queryOptions({
    queryKey: seasonBindingKeys.detail(id),
    queryFn: () => legacyGetSeasonBinding(id),
  })
}

/** 旧的 useSeasonBinding：只用于合集的季绑定，详情是旧的条目表 */
export function useLegacySeasonBinding(
  binding: CollectionSeasonBinding,
  summaryUpdatedAt: number,
  expanded: boolean,
) {
  const reloadSeries = useReloadSeries()
  const query = useQuery({
    ...legacySeasonBindingOptions(binding.id),
    enabled: (q) => expanded || binding.running || isRunning(q.state.data),
    refetchInterval: (q) => (isRunning(q.state.data) ? pollInterval : false),
  })
  const { refetch } = query
  const latest = query.data && query.dataUpdatedAt >= summaryUpdatedAt ? query.data : null
  const view = latest?.kind === 'collection' ? latest : binding
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

/** 旧的 useWatchSeasonBinding：放进旧的详情，没给时重新取一次 */
export function useLegacyWatchSeasonBinding() {
  const queryClient = useQueryClient()
  return useCallback(
    async (id: number, detail?: LegacySeasonBindingDetail) => {
      if (detail) {
        queryClient.setQueryData(seasonBindingKeys.detail(id), detail)
      } else {
        await queryClient.fetchQuery(legacySeasonBindingOptions(id))
      }
    },
    [queryClient],
  )
}
