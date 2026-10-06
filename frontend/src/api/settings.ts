import { request } from './request'

/** 目录源的配置，不含 API key */
export interface CatalogSourceSettings {
  kind: string
  url: string
  libraries: string[]
}

/** 追更的时间规则，单位秒 */
export interface FollowSettings {
  /** 后台扫描的间隔：目录同步进来新的集后最迟多久补建 */
  scanInterval: number
  /** 检查合集的周期，也是自动重新拉取的最短间隔 */
  checkInterval: number
  /** 绑定建出后多久之内自动重新拉取，0 表示不自动重新拉取 */
  refetchWindow: number
}

/** 只读的配置，对应后端 GET /api/settings */
export interface Settings {
  /** 弹弹 API 的 token，null 表示没有设置 */
  dandanplayToken: string | null
  /** null 表示未配置目录源 */
  catalogSource: CatalogSourceSettings | null
  /** 定时同步的间隔，单位秒，0 表示关闭 */
  syncInterval: number
  /** 是否配置了 B 站的 SESSDATA；不返回它的值 */
  bilibiliSessdataConfigured: boolean
  follow: FollowSettings
}

export function getSettings() {
  return request<Settings>({ url: '/settings' })
}
