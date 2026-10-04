import { useQuery } from '@tanstack/react-query'

import { getSeries, listSeries } from '@/api/series'

/**
 * 查询键：剧列表 ['series']，剧详情 ['series', id]。
 * 剧详情以剧列表的键为前缀，让 seriesKeys.list 失效会连同已加载的剧详情一起刷新。
 */
export const seriesKeys = {
  list: ['series'] as const,
  detail: (id: number) => ['series', id] as const,
}

/** 全部剧，不分页 */
export function useSeriesList() {
  return useQuery({ queryKey: seriesKeys.list, queryFn: listSeries })
}

/** 一部剧的完整子树；不存在时 error 为 status 404 的 ApiError */
export function useSeries(id: number) {
  return useQuery({ queryKey: seriesKeys.detail(id), queryFn: () => getSeries(id) })
}
