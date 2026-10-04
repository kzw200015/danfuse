import { request } from './request'

export type BindingStatus = 'active' | 'dead'

/** 绑定：一集与一个弹幕源的对应关系，对应后端 service.BindingView */
export interface Binding {
  id: number
  adapter: string
  /** 弹幕源在平台上的地址 */
  sourceUrl: string
  /** 例如"B 站投稿 BV1xx411c7XX P2" */
  sourceLabel: string
  /** 弹幕源的标题 */
  title: string
  /** 弹幕源视频的时长，秒 */
  duration: number
  /** 秒，正数表示弹幕延后 */
  offset: number
  /** dead 表示上次拉取时弹幕源已不存在，已保存的弹幕仍照常输出 */
  status: BindingStatus
  danmakuCount: number
  lastFetchedAt: string | null
}

/**
 * 创建绑定时后端当场拉取全部弹幕，最长约 25 秒（服务端的写超时是 30 秒）；
 * 默认的 15 秒请求超时不够，放宽到 35 秒，让服务端先给出结果。
 */
const createTimeout = 35_000

/** 贴链接给一集创建绑定，当场拉取全部弹幕；拉取失败时不创建 */
export function createBinding(episodeId: number, url: string) {
  return request<Binding>({
    url: `/episodes/${episodeId}/bindings`,
    method: 'POST',
    data: { url },
    timeout: createTimeout,
  })
}
