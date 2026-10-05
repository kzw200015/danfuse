import { request, slowRequestTimeout } from './request'

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
  /** 建出这个绑定的季绑定；手动贴链接建的、或季绑定已被删除的为 null */
  seasonBindingId: number | null
}

/** 贴链接给一集创建绑定，当场拉取全部弹幕；拉取失败时不创建 */
export function createBinding(episodeId: number, url: string) {
  return request<Binding>({
    url: `/episodes/${episodeId}/bindings`,
    method: 'POST',
    data: { url },
    timeout: slowRequestTimeout,
  })
}

export interface RefetchResult {
  binding: Binding
  /** 新增条数；清空后重新拉取时为这次的总条数 */
  added: number
}

/**
 * 重新拉取一个绑定的全部弹幕。clear 为 false 时只插入新弹幕；为 true 时拉取成功后替换现有弹幕。
 * 拉取失败时不改动弹幕；弹幕源不存在（422）时后端把绑定标为失效。
 */
export function refetchBinding(id: number, clear: boolean) {
  return request<RefetchResult>({
    url: `/bindings/${id}/refetch`,
    method: 'POST',
    data: { clear },
    timeout: slowRequestTimeout,
  })
}

/** 改偏移（秒，正数表示弹幕延后） */
export function updateBindingOffset(id: number, offset: number) {
  return request<Binding>({ url: `/bindings/${id}`, method: 'PATCH', data: { offset } })
}

/** 删除绑定，它的弹幕一起删除 */
export function deleteBinding(id: number) {
  return request<null>({ url: `/bindings/${id}`, method: 'DELETE' })
}
