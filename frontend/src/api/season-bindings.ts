import { request, slowRequestTimeout } from './request'

export type SeasonBindingStatus = 'active' | 'dead'

/** 季绑定：一季与一个合集的对应关系，对应后端 service.SeasonBindingView */
export interface SeasonBinding {
  id: number
  seasonId: number
  adapter: string
  /** 合集在平台上的地址 */
  sourceUrl: string
  /** 例如"B 站番剧 ss41410"，含合集的种类 */
  sourceLabel: string
  /** 合集标题，每次检查更新 */
  title: string
  /** 平台上已完结 */
  finished: boolean
  /** 集号对应：合集第 mappingFrom 集为本地第 mappingTo 集，之后一一顺延 */
  mappingFrom: number
  mappingTo: number
  /** 合集的序号由集号规则从条目的标题认出（投稿合集、多 P 投稿） */
  numberedByRule: boolean
  /** 集号规则：按优先级排列的正则，第一条匹配上的给出集号 */
  episodePatterns: string[]
  /** 追更：定期检查合集、同步后补建、新建出的绑定定期重新拉取，时间规则见 Settings.follow */
  follow: boolean
  /** dead 表示上次检查时合集已不存在 */
  status: SeasonBindingStatus
  /** 上次检查结束时的错误 */
  lastError: string | null
  /** 上次检查的开始时间；还没检查过时为 null */
  lastCheckedAt: string | null
  /** 正在补建 */
  running: boolean
  /** 它建出的、现存的绑定数 */
  bindingCount: number
}

export type SeasonBindingItemState =
  | 'bound'
  | 'alreadyBound'
  | 'bindingDeleted'
  | 'unmatched'
  | 'beforeStart'
  | 'waitingEpisode'
  | 'pending'
  | 'failed'

/** 条目表的一行：上次检查时合集里的一个条目 */
export interface SeasonBindingItem {
  label: string
  /** 合集序号；对不上时为 null */
  number: number | null
  state: SeasonBindingItemState
  /** 对不上、失败的原因 */
  reason: string | null
  /** 季绑定建出过绑定的条目为绑定建在的那一集；其余能算出对应的集时为对应的集号 */
  episodeNumber: number | null
  /** 失败的时间 */
  lastErrorAt: string | null
}

/** 季绑定的详情，含条目表（按在合集里的顺序） */
export interface SeasonBindingDetail extends SeasonBinding {
  items: SeasonBindingItem[]
}

export interface Mapping {
  from: number
  to: number
}

/** 预览的条目，字段与季绑定的条目相同；重复的序号已标为对不上 */
export interface PreviewItem {
  label: string
  number: number | null
  /** 对不上的原因 */
  reason: string | null
}

/** 链接识别出的一个候选合集 */
export interface CollectionCandidate {
  /** 创建时传回，用来选候选 */
  kind: string
  title: string
  sourceUrl: string
  sourceLabel: string
  finished: boolean
  /** 序号由集号规则认出：预览按请求里的规则认，创建时传同一个规则 */
  numberedByRule: boolean
  items: PreviewItem[]
}

export interface CollectionPreview {
  candidates: CollectionCandidate[]
}

/** 预览一季要绑定的合集，按集号规则认出序号，不保存任何东西 */
export function previewSeasonBinding(seasonId: number, link: string, episodePatterns: string[]) {
  return request<CollectionPreview>({
    url: `/seasons/${seasonId}/season-bindings/preview`,
    method: 'POST',
    data: { link, episodePatterns },
    timeout: slowRequestTimeout,
  })
}

export interface CreateSeasonBinding {
  link: string
  /** 链接只有一个候选时可以不传 */
  kind?: string
  mappingFrom: number
  mappingTo: number
  /** 集号规则，与预览时用的相同 */
  episodePatterns: string[]
}

/** 创建季绑定，返回详情；补建随即在后台进行 */
export function createSeasonBinding(seasonId: number, data: CreateSeasonBinding) {
  return request<SeasonBindingDetail>({
    url: `/seasons/${seasonId}/season-bindings`,
    method: 'POST',
    data,
    timeout: slowRequestTimeout,
  })
}

/** 季绑定的详情，不请求平台 */
export function getSeasonBinding(id: number) {
  return request<SeasonBindingDetail>({ url: `/season-bindings/${id}` })
}

/** 改季绑定：只改传了的字段 */
export interface SeasonBindingPatch {
  follow?: boolean
  mappingFrom?: number
  mappingTo?: number
  episodePatterns?: string[]
}

/** 开关追更、改集号对应和集号规则，只改传了的字段。改了规则时返回的条目已按新规则认出序号；打开追更或改了对应、规则时后端随即在后台补建 */
export function updateSeasonBinding(id: number, patch: SeasonBindingPatch) {
  return request<SeasonBindingDetail>({
    url: `/season-bindings/${id}`,
    method: 'PATCH',
    data: patch,
  })
}

/** 立即在后台补建；正在补建时抛出 status 为 409 的 ApiError */
export function backfillSeasonBinding(id: number) {
  return request<null>({ url: `/season-bindings/${id}/backfill`, method: 'POST' })
}

/** 删除季绑定；withBindings 时一起删除它建出的绑定，否则那些绑定变成普通绑定 */
export function deleteSeasonBinding(id: number, withBindings: boolean) {
  return request<null>({
    url: `/season-bindings/${id}`,
    method: 'DELETE',
    params: { withBindings },
  })
}

/** 默认的集号规则：贴链接预览时作为编辑的起点，"恢复默认"时取它 */
export function getDefaultEpisodePatterns() {
  return request<{ episodePatterns: string[] }>({ url: '/episode-rules/default' })
}
