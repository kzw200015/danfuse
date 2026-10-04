import type { Binding } from './bindings'
import { request } from './request'

export type SeriesType = 'tv' | 'movie'

/** 剧列表的一项 */
export interface SeriesSummary {
  id: number
  type: SeriesType
  title: string
  originalTitle: string | null
  year: number | null
  /** 海报的图片 ID，没有海报时为 null；图片地址见 ./images.ts 的 imageUrl */
  posterImageId: number | null
  seasonCount: number
  episodeCount: number
  /** 至少有一个绑定的集数 */
  boundEpisodeCount: number
  bindingCount: number
  deadBindingCount: number
}

export interface Episode {
  id: number
  number: number
  title: string | null
  /** 秒 */
  duration: number | null
  /** 按创建顺序 */
  bindings: Binding[]
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
  /** 海报的图片 ID，没有海报时为 null */
  posterImageId: number | null
  /** 按季号排序 */
  seasons: Season[]
}

/** 全部剧，不分页 */
export function listSeries() {
  return request<SeriesSummary[]>({ url: '/series' })
}

/** 一部剧的完整子树；不存在时抛出 status 为 404 的 ApiError */
export function getSeries(id: number) {
  return request<SeriesDetail>({ url: `/series/${id}` })
}
