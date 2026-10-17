import type { Binding } from './bindings'
import { request } from './request'
import type { SeasonBinding } from './season-bindings'

export type SeriesType = 'tv' | 'movie'

/** 剧列表的一项 */
export interface SeriesSummary {
  id: number
  type: SeriesType
  title: string
  originalTitle: string | null
  year: number | null
  /** 首播日期，形如 2019-04-06；没有时为 null。列表按它倒序 */
  premiereDate: string | null
  /** TMDB 上的编号，没有时为 null，列表上标出警告 */
  tmdbId: number | null
  /** 海报的图片 ID，没有海报时为 null；图片地址见 ./images.ts 的 imageUrl */
  posterImageId: number | null
  seasonCount: number
  episodeCount: number
  /** 至少有一个绑定的集数 */
  boundEpisodeCount: number
  bindingCount: number
  deadBindingCount: number
  /** 有开着追更的季绑定 */
  following: boolean
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
  /** 按创建顺序，不含条目表 */
  seasonBindings: SeasonBinding[]
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
  /** 首播日期，形如 2019-04-06；没有时为 null */
  premiereDate: string | null
  /** TMDB 上的编号，没有时为 null；有它的剧同步时以它为身份 */
  tmdbId: number | null
  /** 海报的图片 ID，没有海报时为 null */
  posterImageId: number | null
  /** 目录源里这部剧的页面；还没有目录源 ID（下次同步补上）、或未配置目录源时为 null */
  sourceLink: CatalogSourceLink | null
  /** 按季号排序 */
  seasons: Season[]
}

/** 目录源里一部剧的页面 */
export interface CatalogSourceLink {
  url: string
  /** 目录源的名称，例如 Jellyfin */
  label: string
}

/** 全部剧，不分页 */
export function listSeries() {
  return request<SeriesSummary[]>({ url: '/series' })
}

/** 一部剧的完整子树；不存在时抛出 status 为 404 的 ApiError */
export function getSeries(id: number) {
  return request<SeriesDetail>({ url: `/series/${id}` })
}

/** 删除一部剧，它的季、集、绑定、弹幕和海报一起删除 */
export function deleteSeries(id: number) {
  return request<null>({ url: `/series/${id}`, method: 'DELETE' })
}

/** 删除一季，它的集、绑定和弹幕一起删除 */
export function deleteSeason(id: number) {
  return request<null>({ url: `/seasons/${id}`, method: 'DELETE' })
}

/** 删除一集，它的绑定和弹幕一起删除 */
export function deleteEpisode(id: number) {
  return request<null>({ url: `/episodes/${id}`, method: 'DELETE' })
}
