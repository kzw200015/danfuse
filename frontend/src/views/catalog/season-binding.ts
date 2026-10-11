import type {
  ChangeCandidate,
  CollectionCandidate,
  CollectionSeasonBinding,
  Mapping,
  PreviewItem,
  SeasonBinding,
  SeasonBindingItem,
  SeasonBindingPatch,
} from '@/api/season-bindings'
import type { Episode } from '@/api/series'

/** 季绑定的名称：文件夹的季绑定为文件夹名；合集的季绑定为合集标题，标题为空时用合集的标签 */
export function seasonBindingName(sb: SeasonBinding) {
  return sb.kind === 'folder' ? sb.title : sb.title || sb.sourceLabel
}

/**
 * 绑定卡片上季绑定的标签：文字写季绑定的名称，悬停提示写全名和来源。seasonBindings 是这一季的季绑定（剧详情里带着）；
 * 不是季绑定建出的绑定为 null，找不到对应的季绑定时（理论上不会发生）只写"季绑定"
 */
export function seasonBindingTag(seasonBindingId: number | null, seasonBindings: SeasonBinding[]) {
  if (seasonBindingId === null) return null
  const sb = seasonBindings.find((s) => s.id === seasonBindingId)
  if (!sb) return { text: '季绑定', title: '由季绑定建出' }
  const name = seasonBindingName(sb)
  return {
    text: `季绑定 · ${name}`,
    title:
      sb.kind === 'folder'
        ? `由文件夹的季绑定「${name}」建出`
        : `由合集的季绑定「${name}」按集号对应建出`,
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

/** 集号对应的上限：与后端的 int 一致，九位数以内 */
const maxMappingDigits = 9

/** 集号对应输入框里的数字：不小于 0 的整数，否则为 null（空的、负数、小数、太大的都不算），在前端拦下，不发请求 */
export function parseMappingNumber(text: string) {
  const s = text.trim()
  return new RegExp(`^\\d{1,${maxMappingDigits}}$`).test(s) ? Number(s) : null
}

/** 预览表格里一个条目会对到哪里 */
export type PreviewTarget =
  | { kind: 'unmatched'; reason: string }
  | { kind: 'beforeStart' }
  | { kind: 'waiting'; episode: number }
  | { kind: 'episode'; episode: Episode }

/**
 * 按集号对应现算：序号 n 不小于 from 的条目对到本地第 n − from + to 集，在本季的集里找这一集；序号小于 from 的不参与。
 * 对不上的原因（集号不是整数、集号重复）由后端在预览里给出，这里不重复判定。
 */
export function previewTarget(
  item: PreviewItem,
  mapping: Mapping,
  episodes: Episode[],
): PreviewTarget {
  if (item.number === null) {
    return { kind: 'unmatched', reason: item.reason ?? '对不上' }
  }
  if (item.number < mapping.from) {
    return { kind: 'beforeStart' }
  }
  const number = item.number - mapping.from + mapping.to
  const episode = episodes.find((e) => e.number === number)
  return episode ? { kind: 'episode', episode } : { kind: 'waiting', episode: number }
}

/** 预览表格里"对到"一栏的样式；target 为 null（集号对应不合法）时同对不到集 */
export function previewTargetClass(target: PreviewTarget | null) {
  switch (target?.kind) {
    case 'episode':
      return 'text-foreground'
    case 'unmatched':
      return 'text-amber-700'
    default:
      return 'text-muted-foreground'
  }
}

/** 预览表格里"对到"一栏的文字 */
export function previewTargetText(target: PreviewTarget) {
  switch (target.kind) {
    case 'unmatched':
      return `对不上：${target.reason}`
    case 'beforeStart':
      return '在起点之前'
    case 'waiting':
      return `目录里还没有第 ${target.episode} 集`
    case 'episode': {
      const n = target.episode.bindings.length
      return `第 ${target.episode.number} 集${n > 0 ? `（已有 ${n} 个绑定）` : ''}`
    }
  }
}

/** 条目表里一个条目的状态文字 */
export function itemStateText(item: SeasonBindingItem) {
  const episode = item.episodeNumber === null ? '' : `第 ${item.episodeNumber} 集`
  switch (item.state) {
    case 'bound':
      return `已建绑定：${episode}`
    case 'alreadyBound':
      return `集上已有这个弹幕源：${episode}`
    case 'bindingDeleted':
      return `绑定已被删除（${episode}）`
    case 'unmatched':
      return `对不上：${item.reason ?? ''}`
    case 'beforeStart':
      return '在起点之前'
    case 'waitingEpisode':
      return `等待目录里出现${episode}`
    case 'pending':
      return `待补建：${episode}`
    case 'failed':
      return `最近一次失败：${item.reason ?? ''}`
  }
}

/**
 * 链接对应多个合集时，候选的说明。B 站的稿件既有多个分 P、又属于合集时，两个候选分别是"这个稿件的 N 个分 P"
 * 和"它所在的合集"（种类标识 multiPage、ugcSeason 由 B 站适配器给出）；其他种类写出合集的标签、标题和条数。
 */
export function candidateText(c: CollectionCandidate | ChangeCandidate) {
  const n = c.items.length
  switch (c.kind) {
    case 'multiPage':
      return `这个稿件《${c.title}》的 ${n} 个分 P`
    case 'ugcSeason':
      return `它所在的合集《${c.title}》共 ${n} 条`
    default:
      return `${c.sourceLabel}《${c.title}》共 ${n} 条`
  }
}

/** 编辑时链接输入框的初始值：记下的链接；记下链接之前建的季绑定用合集的链接代替 */
export function editLink(sb: CollectionSeasonBinding) {
  return sb.link ?? sb.sourceUrl
}

/** 编辑表单里要保存的内容：预览用的链接、选中的候选、集号对应和预览用的集号规则 */
export interface SeasonBindingEdit {
  link: string
  candidate: Pick<ChangeCandidate, 'kind' | 'current'>
  mapping: Mapping
  patterns: string[]
}

/**
 * 编辑季绑定要发出的改动，只带改了的字段，没有改动时为空对象。换了链接或选了别的候选时带上 link 和 kind
 * （同一个合集换了一种链接时后端只改记下的链接）；集号规则逐条比较，调整顺序也算改动
 */
export function seasonBindingPatch(sb: CollectionSeasonBinding, edit: SeasonBindingEdit) {
  const patch: SeasonBindingPatch = {}
  if (edit.link !== editLink(sb) || !edit.candidate.current) {
    patch.link = edit.link
    patch.kind = edit.candidate.kind
  }
  if (edit.mapping.from !== sb.mappingFrom || edit.mapping.to !== sb.mappingTo) {
    patch.mappingFrom = edit.mapping.from
    patch.mappingTo = edit.mapping.to
  }
  if (!samePatterns(edit.patterns, sb.episodePatterns)) patch.episodePatterns = edit.patterns
  return patch
}
