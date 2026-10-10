import type { Mapping, PreviewItem } from '@/api/season-bindings'
import type { Episode } from '@/api/series'

/** 按季上传的一个条目：一个子目录，或顶层平铺的一份文件 */
export interface UploadEntry {
  /** 条目名称："文件夹名 / 子目录名"或"文件夹名 / 文件名去掉扩展名" */
  label: string
  files: File[]
  /** 与 files 一一对应、顺序相同的相对路径，以所选文件夹名开头 */
  paths: string[]
}

/** 分组的结果：条目与忽略的（不是 .xml 的）份数，或给用户看的错误（超过两层、没有 XML、条目名称重复），出错时不进入预览 */
export type FolderGrouping =
  | { ok: true; entries: UploadEntry[]; ignored: number }
  | { ok: false; error: string }

const naturalOrder = new Intl.Collator(undefined, { numeric: true })

function stripExtension(name: string) {
  const dot = name.lastIndexOf('.')
  return dot > 0 ? name.slice(0, dot) : name
}

/** 把目录选择选出的文件按相对路径（webkitRelativePath）分成条目 */
export function groupFolderFiles(files: readonly File[]): FolderGrouping {
  // 浏览器给出的顺序不确定，先按路径的自然顺序排好（2 在 10 前面），条目里的文件与"第一份越界文件"都按这个顺序
  const sorted = files.toSorted((a, b) =>
    naturalOrder.compare(a.webkitRelativePath, b.webkitRelativePath),
  )
  const entries = new Map<string, UploadEntry>()
  let ignored = 0
  for (const file of sorted) {
    const path = file.webkitRelativePath
    if (!path.toLowerCase().endsWith('.xml')) {
      ignored++
      continue
    }
    // 目录选择给出的路径以所选文件夹名开头：文件夹/x.xml 或 文件夹/子目录/x.xml
    const [folder = '', child = '', ...rest] = path.split('/')
    if (rest.length > 1) {
      return {
        ok: false,
        error: `目录最多两层（文件夹 / 子目录 / 文件），这份文件超出了：${path}`,
      }
    }
    // 子目录里的文件按子目录合成一个条目，顶层平铺的文件各自一个条目
    const flat = rest.length === 0
    const key = flat ? path : `${folder}/${child}/`
    let entry = entries.get(key)
    if (!entry) {
      const name = flat ? stripExtension(child) : child
      entry = { label: `${folder} / ${name}`, files: [], paths: [] }
      entries.set(key, entry)
    }
    entry.files.push(file)
    entry.paths.push(path)
  }
  if (entries.size === 0) {
    return { ok: false, error: '所选文件夹里没有 XML 文件' }
  }
  const labels = new Set<string>()
  for (const { label } of entries.values()) {
    if (labels.has(label)) {
      return { ok: false, error: `条目名称重复：${label}` }
    }
    labels.add(label)
  }
  return {
    ok: true,
    entries: [...entries.values()].toSorted((a, b) => naturalOrder.compare(a.label, b.label)),
    ignored,
  }
}

/**
 * 按季上传预览时预填的集号对应：取认出的最小集号，本季有同号的集时同号对应，否则对到本季最小的集号；
 * 本季还没有集时同号对应，一个都认不出时为 1 = 1。季绑定不用它（那边预填 1 = 1）
 */
export function defaultMapping(items: PreviewItem[], episodes: Episode[]): Mapping {
  const numbers = items.flatMap((it) => (it.number === null ? [] : [it.number]))
  if (numbers.length === 0) {
    return { from: 1, to: 1 }
  }
  const from = Math.min(...numbers)
  if (episodes.length === 0 || episodes.some((e) => e.number === from)) {
    return { from, to: from }
  }
  return { from, to: Math.min(...episodes.map((e) => e.number)) }
}
