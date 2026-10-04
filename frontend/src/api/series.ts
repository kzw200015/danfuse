import { request } from './request'

export type SeriesType = 'tv' | 'movie'

/** 剧列表的一项 */
export interface SeriesSummary {
  id: number
  type: SeriesType
  title: string
  originalTitle: string | null
  year: number | null
  seasonCount: number
  episodeCount: number
}

export interface Episode {
  id: number
  number: number
  title: string | null
  /** 秒 */
  duration: number | null
}

/** 季，第 0 季是特别篇 */
export interface Season {
  id: number
  number: number
  title: string | null
  /** 按集号排序 */
  episodes: Episode[]
}

/** 一部剧的完整子树 */
export interface SeriesDetail {
  id: number
  type: SeriesType
  title: string
  originalTitle: string | null
  year: number | null
  /** 按季号排序 */
  seasons: Season[]
}

/**
 * 查询键：剧列表 ['series']，剧详情 ['series', id]。
 * 剧详情以剧列表的键为前缀，让 ['series'] 失效会连同已加载的剧详情一起刷新。
 */
export const seriesKeys = {
  list: ['series'] as const,
  detail: (id: number) => ['series', id] as const,
}

/** 全部剧，不分页 */
export function listSeries() {
  return request<SeriesSummary[]>({ url: '/series' })
}

/** 一部剧的完整子树；不存在时抛出 status 为 404 的 ApiError */
export function getSeries(id: number) {
  return request<SeriesDetail>({ url: `/series/${id}` })
}
