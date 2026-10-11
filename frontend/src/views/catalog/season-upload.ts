import type { EntryHashes } from '@/api/season-bindings'
import { sha256 as sha256Bytes } from '@/lib/sha256'

import { naturalOrder } from './catalog'

/** 文件夹里的一个条目：一个子目录，或顶层平铺的一份文件 */
export interface UploadEntry {
  /** 条目名称，不带文件夹名：子目录名，或去掉扩展名的文件名 */
  label: string
  files: File[]
  /** 与 files 一一对应、顺序相同的相对路径，以所选文件夹名开头 */
  paths: string[]
}

/**
 * 分组的结果：所选的文件夹名、条目与忽略的（不是 .xml 的）份数，或给用户看的错误（超过两层、没有 XML、条目名称重复），
 * 出错时不进入预览。后端按同一套规则校验
 */
export type FolderGrouping =
  | { ok: true; folder: string; entries: UploadEntry[]; ignored: number }
  | { ok: false; error: string }

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
  let folderName = ''
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
      entry = { label: name, files: [], paths: [] }
      entries.set(key, entry)
    }
    entry.files.push(file)
    entry.paths.push(path)
    folderName = folder
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
    folder: folderName,
    entries: [...entries.values()].toSorted((a, b) => naturalOrder.compare(a.label, b.label)),
    ignored,
  }
}

/**
 * 一份文件内容的 SHA-256，小写十六进制。优先用 WebCrypto；它只在安全上下文（HTTPS、localhost）里有，
 * 自托管用 http://局域网 IP 打开时没有，改用纯 JS 的实现
 */
async function sha256(file: File) {
  const content = await file.arrayBuffer()
  const digest = globalThis.crypto?.subtle
    ? new Uint8Array(await crypto.subtle.digest('SHA-256', content))
    : sha256Bytes(new Uint8Array(content))
  return Array.from(digest, (b) => b.toString(16).padStart(2, '0')).join('')
}

/**
 * 算出各条目每份文件的 SHA-256（顺序与文件相同），预览时代替文件本身发给后端：
 * 编辑时后端按"名称 + 文件哈希的集合"与保存的条目对上
 */
export async function hashEntries(entries: readonly UploadEntry[]): Promise<EntryHashes[]> {
  return Promise.all(
    entries.map(async (e) => ({ label: e.label, sha256s: await Promise.all(e.files.map(sha256)) })),
  )
}
