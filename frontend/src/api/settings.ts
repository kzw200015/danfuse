import { request } from './request'

/** 目录源的配置，不含 API key */
export interface CatalogSourceSettings {
  kind: string
  url: string
  libraries: string[]
}

/** 只读的配置，对应后端 GET /api/settings */
export interface Settings {
  /** null 表示未配置目录源 */
  catalogSource: CatalogSourceSettings | null
  /** 定时同步的间隔，单位秒，0 表示关闭 */
  syncInterval: number
}

export function getSettings() {
  return request<Settings>({ url: '/settings' })
}
