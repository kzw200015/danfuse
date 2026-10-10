import { request, slowRequestTimeout, uploadRequestTimeout } from './request'
import type { PreviewItem } from './season-bindings'

export type BindingStatus = 'active' | 'dead'

/** 绑定：一集与一个弹幕源的对应关系，对应后端 service.BindingView；按 kind 区分弹幕源的形态 */
export type Binding = LinkBinding | FileBinding

/** 贴链接（或季绑定补建）建的绑定 */
export interface LinkBinding extends BindingBase {
  kind: 'link'
  /** 源适配器的 ID */
  adapter: string
  /** 弹幕源在平台上的地址 */
  sourceUrl: string
  /** 弹幕源视频的时长，秒 */
  duration: number
}

/** 上传弹幕文件建的绑定：没有适配器、链接和时长，不会失效 */
export interface FileBinding extends BindingBase {
  kind: 'file'
  adapter: null
  sourceUrl: null
  duration: null
}

interface BindingBase {
  id: number
  /** 例如"B 站投稿 BV1xx411c7XX P2"、"弹幕文件 · 5 份" */
  sourceLabel: string
  /** 弹幕源的标题；用弹幕文件建的取第一份文件的文件名 */
  title: string
  /** 秒，正数表示弹幕延后 */
  offset: number
  /** dead 表示上次拉取时弹幕源已不存在，已保存的弹幕仍照常输出 */
  status: BindingStatus
  danmakuCount: number
  /** 弹幕内容的版本：插入了新弹幕、或替换了全部弹幕时加 1，改偏移不变 */
  contentVersion: number
  /** 最晚一条弹幕的时间，毫秒，未校正；没有弹幕时为 0 */
  maxTimeMs: number
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

/** 一组文件放进 multipart 请求的 files 字段 */
function filesForm(files: File[]) {
  const form = new FormData()
  for (const f of files) form.append('files', f)
  return form
}

/**
 * 上传弹幕文件给一集创建绑定：一次上传的几份文件合起来是一个弹幕源，后端当场解析、保存。
 * 有一份认不出就不创建（422）。上传的文件可能有几十 MB，不设超时。
 */
export function createFileBinding(episodeId: number, files: File[]) {
  return request<Binding>({
    url: `/episodes/${episodeId}/file-bindings`,
    method: 'POST',
    data: filesForm(files),
    timeout: uploadRequestTimeout,
  })
}

/** 按季上传的预览：按集号规则从条目名称认出集号，不上传文件、不保存任何东西 */
export function previewSeasonFileBindings(
  seasonId: number,
  labels: string[],
  episodePatterns: string[],
) {
  return request<{ items: PreviewItem[] }>({
    url: `/seasons/${seasonId}/file-bindings/preview`,
    method: 'POST',
    data: { labels, episodePatterns },
  })
}

/** 按季上传的一个条目对到的集 */
export interface SeasonUploadTarget {
  label: string
  episodeId: number
}

export interface SeasonUpload {
  /** 勾选的条目的全部文件 */
  files: File[]
  /** 与 files 一一对应、顺序相同的相对路径，以所选文件夹名开头 */
  paths: string[]
  /** 每个条目一项 */
  targets: SeasonUploadTarget[]
}

export interface SeasonUploadResult {
  /** 建出的绑定数 */
  bindings: number
  /** 新增的弹幕总条数 */
  added: number
}

/**
 * 按季上传：每个条目各建一个用弹幕文件建的绑定，在一个事务里，要么全部建出、要么一个都不建。
 * paths、targets 各是一个 JSON 字段（逐份一个字段会超出后端 multipart 的 part 数上限）。
 * 一季的文件可能有上百 MB，不设超时；onProgress 收到上传的进度（0～1）
 */
export function createSeasonFileBindings(
  seasonId: number,
  { files, paths, targets }: SeasonUpload,
  onProgress?: (progress: number) => void,
) {
  const form = filesForm(files)
  form.append('paths', JSON.stringify(paths))
  form.append('targets', JSON.stringify(targets))
  return request<SeasonUploadResult>({
    url: `/seasons/${seasonId}/file-bindings`,
    method: 'POST',
    data: form,
    timeout: uploadRequestTimeout,
    onUploadProgress: (e) => onProgress?.(e.progress ?? (e.total ? e.loaded / e.total : 0)),
  })
}

export interface AppendFilesResult {
  binding: Binding
  /** 新加入的份数 */
  files: number
  /** 绑定里已有、跳过的份数（按内容判断） */
  skipped: number
  /** 新增的弹幕条数 */
  added: number
}

/** 追加文件：往用弹幕文件建的绑定里再加入弹幕文件，只增不删；有一份认不出就什么都不加（422） */
export function appendBindingFiles(id: number, files: File[]) {
  return request<AppendFilesResult>({
    url: `/bindings/${id}/files`,
    method: 'POST',
    data: filesForm(files),
    timeout: uploadRequestTimeout,
  })
}

/** 重新解析：按保存的全部弹幕文件重新解析，替换现有弹幕 */
export function reparseBinding(id: number) {
  return request<Binding>({
    url: `/bindings/${id}/reparse`,
    method: 'POST',
    timeout: slowRequestTimeout,
  })
}

/** 绑定里的一份弹幕文件 */
export interface BindingFile {
  name: string
  /** 字节 */
  size: number
  uploadedAt: string
}

/** 用弹幕文件建的绑定里的文件，按加入的顺序 */
export function listBindingFiles(id: number) {
  return request<BindingFile[]>({ url: `/bindings/${id}/files` })
}

/** 弹幕的类型，沿用 B 站的编号：1 滚动、4 底部、5 顶部、6 逆向 */
export type DanmakuMode = 1 | 4 | 5 | 6

/** 绑定保存的一条弹幕 */
export interface DanmakuItem {
  /** 弹幕源时间：相对弹幕源视频开头的毫秒数，未校正 */
  timeMs: number
  mode: DanmakuMode
  /** RGB888 */
  color: number
  text: string
}

export interface DanmakuPage {
  items: DanmakuItem[]
  /** 下一页的游标，没有下一页时为 null */
  next: string | null
}

/**
 * 绑定保存的弹幕，按弹幕源时间升序分页。fromMs 用于跳转：只取弹幕源时间在它及以后的，翻页时照传；
 * after 为上一页的游标，不传时从头取。
 */
export function listBindingDanmaku(id: number, fromMs: number | null, after?: string) {
  return request<DanmakuPage>({
    url: `/bindings/${id}/danmaku`,
    params: { fromMs: fromMs ?? undefined, after },
  })
}
