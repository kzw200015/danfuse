import type {
  Candidate,
  Item,
  SeasonBinding,
  SeasonBindingKind,
  Summary,
} from '@/api/season-bindings'
import type { Season } from '@/api/series'

import { catalogPath } from './catalog'

/**
 * 条目表（详情，补建的语义）里一个条目的状态文字：对到的集写在前面；替换写出原来的绑定，合集说"待换新稿件"，文件夹说"待替换"。
 * 上次补建失败的原因（item.lastError）不在这里，由条目表另外标出
 */
export function itemStatusText(item: Item, kind: SeasonBindingKind) {
  const text = statusText(item, kind)
  return item.episodeNumber === null ? text : `第 ${item.episodeNumber} 集 · ${text}`
}

function statusText(item: Item, kind: SeasonBindingKind) {
  switch (item.status) {
    case 'keep':
      return '已建绑定'
    case 'create':
      return '待补建'
    case 'replace': {
      const text = kind === 'collection' ? '待换新稿件' : '待替换'
      return item.replaces ? `${text}（原：${item.replaces.title}）` : text
    }
    case 'alreadyBound':
      return '已有同源绑定'
    case 'unmatched':
      return `对不上（${item.reason ?? '认不出集号'}）`
    case 'beforeStart':
      return '在起点之前'
    case 'noEpisode':
      return '目录里没有这一集'
  }
}

/**
 * 预览表"保存后"一列的文字（保存表单的语义）：对到的集写在前面，接着是保存后的状态；替换写出原来的绑定
 */
export function previewStatusText(item: Item) {
  const text = previewText(item)
  return item.episodeNumber === null ? text : `第 ${item.episodeNumber} 集 · ${text}`
}

function previewText(item: Item) {
  switch (item.status) {
    case 'keep':
      return '保留'
    case 'create':
      return '新建'
    case 'replace':
      return item.replaces ? `替换（原：${item.replaces.title}）` : '替换'
    case 'alreadyBound':
      return '跳过：已有同源绑定'
    default:
      return statusText(item, 'folder')
  }
}

/**
 * 链接对应多个合集时，候选的说明。B 站的稿件既有多个分 P、又属于合集时，两个候选分别是"这个稿件的 N 个分 P"
 * 和"它所在的合集"（种类标识 multiPage、ugcSeason 由 B 站适配器给出）；其他种类写出合集的标签、标题和条数。
 */
export function candidateText(c: Candidate) {
  const n = c.items.length
  switch (c.collectionKind) {
    case 'multiPage':
      return `这个稿件《${c.title}》的 ${n} 个分 P`
    case 'ugcSeason':
      return `它所在的合集《${c.title}》共 ${n} 条`
    default:
      return `${c.sourceLabel ?? ''}《${c.title}》共 ${n} 条`
  }
}

/** 预览顶部的汇总标签：新建、替换、删除、保留各几个；删除大于 0 时标红 */
export function summaryTags(summary: Summary) {
  return [
    { text: `新建 ${summary.create}`, destructive: false },
    { text: `替换 ${summary.replace}`, destructive: false },
    { text: `删除 ${summary.remove}`, destructive: summary.remove > 0 },
    { text: `保留 ${summary.keep}`, destructive: false },
  ]
}

/** 季绑定的名称：文件夹的季绑定为文件夹名；合集的季绑定为合集标题，标题为空时用合集的标签 */
export function seasonBindingName(sb: SeasonBinding) {
  return sb.kind === 'folder' ? sb.title : sb.title || sb.sourceLabel
}

/**
 * 绑定卡片上季绑定的锁形标签：文字只写季绑定的名称，悬停提示写全名和来源，to 是季面板的路径（建出的绑定要到那里改）。
 * season 是绑定所在的季（剧 seriesId 的，带着剧详情里的季绑定），季绑定与它建出的绑定总在同一季；
 * path 拼出目录页的地址，默认不带查询参数，组件里传 useCatalogPath() 的结果以保留地址栏的分类。
 * 不是季绑定建出的绑定为 null，找不到对应的季绑定时（理论上不会发生）只写"季绑定"
 */
export function seasonBindingTag(
  seasonBindingId: number | null,
  season: Pick<Season, 'id' | 'seasonBindings'>,
  seriesId: number,
  path: (...ids: number[]) => string = catalogPath,
) {
  if (seasonBindingId === null) return null
  const to = path(seriesId, season.id)
  const sb = season.seasonBindings.find((s) => s.id === seasonBindingId)
  if (!sb) return { text: '季绑定', title: '由季绑定建出', to }
  const name = seasonBindingName(sb)
  return {
    text: name,
    title:
      sb.kind === 'folder'
        ? `由文件夹的季绑定「${name}」建出`
        : `由合集的季绑定「${name}」按集号对应建出`,
    to,
  }
}

/** 集号规则：去掉每条前后的空白、丢掉空的，提交和比较都用它 */
export function cleanPatterns(patterns: string[]) {
  return patterns.map((p) => p.trim()).filter(Boolean)
}

/** 两组集号规则逐条相同（顺序不同也算不同） */
export function samePatterns(a: string[], b: string[]) {
  return a.length === b.length && a.every((p, i) => p === b[i])
}

/** 集号对应的说明（悬停提示）：prefix 是 X 前面的称呼，见 MappingInputs */
export function mappingHint(prefix = '合集') {
  return `${prefix}第 X 集对应本地第 Y 集，之后一一顺延；序号小于 X 的条目不参与`
}

/** 集号对应的上限：与后端的 int 一致，九位数以内 */
const maxMappingDigits = 9

/** 集号对应输入框里的数字：不小于 0 的整数，否则为 null（空的、负数、小数、太大的都不算），在前端拦下，不发请求 */
export function parseMappingNumber(text: string) {
  const s = text.trim()
  return new RegExp(`^\\d{1,${maxMappingDigits}}$`).test(s) ? Number(s) : null
}
