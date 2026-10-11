import { request, slowRequestTimeout, uploadRequestTimeout } from './request'

export type SeasonBindingStatus = 'active' | 'dead'

/**
 * 季绑定（剧详情里的 seasonBindings[]），按 kind 分两种：合集的季绑定（一季与一个合集的对应关系）、
 * 文件夹的季绑定（一季与上传的一个弹幕文件夹的对应关系）。两种都保存着一份表单，各集的绑定按表单建出
 */
export type SeasonBinding = CollectionSeasonBinding | FolderSeasonBinding

/** 两种季绑定都有的字段 */
interface SeasonBindingBase {
  id: number
  seasonId: number
  /** 合集的季绑定为合集标题（每次检查更新，可能为空）；文件夹的季绑定为上传的文件夹名 */
  title: string
  /** 集号规则：一组正则，取条目名称里结束得最靠后的集号，一样靠后时排在前面的优先 */
  episodePatterns: string[]
  /** 集号对应：条目第 mappingFrom 集为本地第 mappingTo 集，之后一一顺延 */
  mappingFrom: number
  mappingTo: number
  /** 它建出的、现存的绑定数 */
  bindingCount: number
  createdAt: string
}

/** 合集的季绑定：按表单在后台补建，可以追更 */
export interface CollectionSeasonBinding extends SeasonBindingBase {
  kind: 'collection'
  adapter: string
  /** 创建或保存时贴的链接，编辑时预填 */
  link: string
  /** 合集在平台上的地址 */
  sourceUrl: string
  /** 例如"B 站番剧 ss41410"，含合集的种类 */
  sourceLabel: string
  /** 合集的序号由集号规则从条目名称认出（投稿合集、多 P 投稿）；番剧的集号由平台给出 */
  numberedByRule: boolean
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
}

/** 文件夹的季绑定：写入都当场完成，不追更、不会失效 */
export interface FolderSeasonBinding extends SeasonBindingBase {
  kind: 'folder'
  /** 保存的条目数 */
  entryCount: number
}

export type SeasonBindingKind = SeasonBinding['kind']

/** 季绑定在一集上的槽位，以及放在里面的、它建出的绑定 */
export interface SlotView {
  episodeNumber: number
  bindingId: number
  /** 绑定的标题 */
  title: string
  /** 绑定已失效 */
  dead: boolean
}

/**
 * 条目对到的结果（派生）：
 * - keep：槽位里已是这个弹幕源的绑定；
 * - create：对到的集没有槽位，新建（合集在后台补建，文件夹当场建出）；
 * - replace：槽位里是别的弹幕源，换成这个条目的（替换，偏移归零）；
 * - alreadyBound：对到的集上已有同一个弹幕源的绑定（只对合集），跳过；
 * - unmatched：对不上，原因见 reason；
 * - beforeStart：序号小于集号对应的起点；
 * - noEpisode：目录里没有对到的那一集
 */
export type ItemStatus =
  | 'keep'
  | 'create'
  | 'replace'
  | 'alreadyBound'
  | 'unmatched'
  | 'beforeStart'
  | 'noEpisode'

/** 一个条目（合集里的一个弹幕源，或文件夹里的一个子目录、顶层的一份文件）派生的结果 */
export interface Item {
  label: string
  /** 序号：番剧为平台给的集号，其余为按集号规则认出的集号；对不上时为 null */
  number: number | null
  /** 对到的本地集号；对不上、在起点之前时为 null */
  episodeNumber: number | null
  status: ItemStatus
  /** unmatched 时为对不上的原因 */
  reason: string | null
  /** replace 时为槽位里原来的绑定 */
  replaces: SlotView | null
  /** 上次补建这个条目失败的原因，成功后清掉；只在详情里有（文件夹恒为 null） */
  lastError?: string | null
  lastErrorAt?: string | null
}

/**
 * 季绑定的详情：items 是各条目按补建的语义派生的结果（不删除），
 * emptySlots 是没有条目对到、绑定还留着的槽位（补建不删，保存一次表单就会删掉）
 */
export type SeasonBindingDetail = CollectionSeasonBindingDetail | FolderSeasonBindingDetail
export type CollectionSeasonBindingDetail = CollectionSeasonBinding & DetailParts
export type FolderSeasonBindingDetail = FolderSeasonBinding & DetailParts

interface DetailParts {
  items: Item[]
  emptySlots: SlotView[]
}

/** 保存后各有几个：新建、替换、删除（没有条目的槽位）、保留 */
export interface Summary {
  create: number
  replace: number
  remove: number
  keep: number
}

/** 预览的一组：合集的一个候选，或文件夹（只有一组） */
export interface Candidate {
  /** 合集的种类标识（如 bangumi、ugcSeason、multiPage），保存时传回来选候选；文件夹为 null */
  collectionKind: string | null
  title: string
  sourceUrl: string | null
  sourceLabel: string | null
  /** 合集在平台上已完结 */
  finished: boolean
  /** 序号由集号规则认出 */
  numberedByRule: boolean
  /** 编辑时标出现在保存的候选 */
  current: boolean
  items: Item[]
  /** 保存后删除的槽位 */
  removed: SlotView[]
  summary: Summary
}

/** 预览：合集按候选各一组，文件夹只有一组。不保存任何东西，合集每次都请求平台 */
export interface Preview {
  candidates: Candidate[]
}

/** 两种表单共有的部分：集号规则与集号对应 */
export interface EpisodeRuleForm {
  episodePatterns: string[]
  mappingFrom: number
  mappingTo: number
}

/** 合集的表单（预览）：贴的链接 */
export interface CollectionPreviewForm extends EpisodeRuleForm {
  link: string
}

/** 合集的表单（创建、保存）：另外带上选中的候选 */
export interface CollectionForm extends CollectionPreviewForm {
  /** 候选的 collectionKind；链接只有一个候选时可以不传 */
  collectionKind?: string
}

/** 文件夹里的一个条目：名称与各份文件的 SHA-256（十六进制），预览时代替文件本身 */
export interface EntryHashes {
  label: string
  sha256s: string[]
}

/** 选的文件夹：文件夹名与它的条目。后端用"文件夹名 / 条目名称"认集号 */
export interface PickedFolder {
  folder: string
  entries: EntryHashes[]
}

/** 文件夹的表单（预览）：编辑时不换文件夹就不传 folder 和 entries，按保存的条目和季绑定的标题算 */
export type FolderPreviewForm = EpisodeRuleForm &
  (PickedFolder | { folder?: never; entries?: never })

/** 预览新建合集的季绑定 */
export function previewNewCollection(seasonId: number, form: CollectionPreviewForm) {
  return request<Preview>({
    url: `/seasons/${seasonId}/season-bindings/collection/preview`,
    method: 'POST',
    data: form,
    timeout: slowRequestTimeout,
  })
}

/** 创建合集的季绑定，返回详情（201）；补建随即在后台进行。同一季已有这个合集时抛出 status 为 409 的 ApiError */
export function createCollection(seasonId: number, form: CollectionForm) {
  return request<CollectionSeasonBindingDetail>({
    url: `/seasons/${seasonId}/season-bindings/collection`,
    method: 'POST',
    data: form,
    timeout: slowRequestTimeout,
  })
}

/** 预览编辑合集的季绑定：candidates 里 current 标出保存的候选 */
export function previewCollectionEdit(id: number, form: CollectionPreviewForm) {
  return request<Preview>({
    url: `/season-bindings/${id}/collection/preview`,
    method: 'POST',
    data: form,
    timeout: slowRequestTimeout,
  })
}

/**
 * 保存合集的季绑定的表单，返回详情：没有条目的槽位已删除，新建、替换随即在后台补建。
 * 正在补建、同一季已有这个合集时抛出 status 为 409 的 ApiError
 */
export function saveCollection(id: number, form: CollectionForm) {
  return request<CollectionSeasonBindingDetail>({
    url: `/season-bindings/${id}/collection`,
    method: 'PUT',
    data: form,
    timeout: slowRequestTimeout,
  })
}

/** 预览新建文件夹的季绑定：只传条目名称和文件哈希，不上传文件 */
export function previewNewFolder(seasonId: number, form: EpisodeRuleForm & PickedFolder) {
  return request<Preview>({
    url: `/seasons/${seasonId}/season-bindings/folder/preview`,
    method: 'POST',
    data: form,
  })
}

/**
 * 文件夹的上传请求：files 是目录选择选出的文件，以它们的相对路径（webkitRelativePath，以所选文件夹名开头）作文件名，
 * 后端按路径分条目；form 是集号规则与集号对应的 JSON
 */
function folderRequest(
  files: File[] | null,
  form: EpisodeRuleForm,
  onProgress?: (progress: number) => void,
) {
  const data = new FormData()
  data.append(
    'form',
    JSON.stringify({
      episodePatterns: form.episodePatterns,
      mappingFrom: form.mappingFrom,
      mappingTo: form.mappingTo,
    }),
  )
  files?.forEach((f) => data.append('files', f, f.webkitRelativePath))
  return {
    data,
    // 一季的文件可能有上百 MB，带文件时不设超时；不带时后端只按保存的条目重新解析
    timeout: files ? uploadRequestTimeout : slowRequestTimeout,
    onUploadProgress: (e: { progress?: number; loaded: number; total?: number }) =>
      onProgress?.(e.progress ?? (e.total ? e.loaded / e.total : 0)),
  }
}

/**
 * 创建文件夹的季绑定，当场建出各集的绑定，返回详情（201）。有一份文件解析失败时抛出 status 为 422 的 ApiError，
 * 什么都不保存；文件夹的结构不对时为 400。onProgress 收到上传的进度（0～1）
 */
export function createFolder(
  seasonId: number,
  files: File[],
  form: EpisodeRuleForm,
  onProgress?: (progress: number) => void,
) {
  return request<FolderSeasonBindingDetail>({
    url: `/seasons/${seasonId}/season-bindings/folder`,
    method: 'POST',
    ...folderRequest(files, form, onProgress),
  })
}

/** 预览编辑文件夹的季绑定：不换文件夹时不传 entries */
export function previewFolderEdit(id: number, form: FolderPreviewForm) {
  return request<Preview>({
    url: `/season-bindings/${id}/folder/preview`,
    method: 'POST',
    data: form,
  })
}

/**
 * 保存文件夹的季绑定的表单，当场完成，返回详情。files 为 null 时不换文件夹，只改集号规则、集号对应；
 * 换文件夹时名称和文件哈希都没变的条目沿用。错误同 createFolder
 */
export function saveFolder(
  id: number,
  files: File[] | null,
  form: EpisodeRuleForm,
  onProgress?: (progress: number) => void,
) {
  return request<FolderSeasonBindingDetail>({
    url: `/season-bindings/${id}/folder`,
    method: 'PUT',
    ...folderRequest(files, form, onProgress),
  })
}

/** 季绑定的详情，不请求平台 */
export function getSeasonBinding(id: number) {
  return request<SeasonBindingDetail>({ url: `/season-bindings/${id}` })
}

/** 开关合集的季绑定的追更，返回详情；打开时后端随即在后台补建 */
export function setFollow(id: number, follow: boolean) {
  return request<CollectionSeasonBindingDetail>({
    url: `/season-bindings/${id}`,
    method: 'PATCH',
    data: { follow },
  })
}

/**
 * 立即补建，返回详情：合集在后台进行（202，详情的 running 为 true），正在补建时抛出 status 为 409 的 ApiError；
 * 文件夹当场完成（200），不删除没有条目的槽位
 */
export function backfillSeasonBinding(id: number) {
  return request<SeasonBindingDetail>({
    url: `/season-bindings/${id}/backfill`,
    method: 'POST',
    timeout: slowRequestTimeout,
  })
}

/** 删除季绑定（两种都可以），它建出的绑定连同弹幕一起删除；文件夹的上传的文件也一起删除 */
export function deleteSeasonBinding(id: number) {
  return request<null>({ url: `/season-bindings/${id}`, method: 'DELETE' })
}

/** 默认的集号规则：编辑的起点，"恢复默认"时取它 */
export function getDefaultEpisodePatterns() {
  return request<{ episodePatterns: string[] }>({ url: '/episode-rules/default' })
}
