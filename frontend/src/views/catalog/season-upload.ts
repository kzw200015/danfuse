import type { EntryHashes } from '@/api/season-bindings'
import { sha256 as sha256Bytes } from '@noble/hashes/sha2.js'
import { bytesToHex } from '@noble/hashes/utils.js'

import { naturalOrder } from './catalog'

/** 文件夹里的一个条目：一个子目录，或顶层平铺的一份文件 */
export interface UploadEntry {
  /** 条目名称，不带文件夹名：子目录名，或去掉扩展名的文件名 */
  label: string
  /** 条目里的文件，各自带着以所选文件夹名开头的相对路径（webkitRelativePath） */
  files: File[]
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

/**
 * 手机端多选 XML 兼容模式使用的虚拟文件夹名。
 * 后端按「文件夹名 / 条目名」匹配集号，因此严禁包含数字，避免误匹配到文件夹名里的数字。
 */
export const MOBILE_UPLOAD_FOLDER = '手机上传'

/** 给平铺多选的文件补上相对路径，兼容后端按 webkitRelativePath 分组 */
function withRelativePath(file: File, relativePath: string): File {
  if (file.webkitRelativePath === relativePath) return file
  const wrapped = new File([file], file.name, {
    type: file.type,
    lastModified: file.lastModified,
  })
  Object.defineProperty(wrapped, 'webkitRelativePath', { value: relativePath })
  return wrapped
}

/** 把目录选择选出的文件按相对路径（webkitRelativePath）分成条目 */
export function groupFolderFiles(files: readonly File[]): FolderGrouping {
  if (files.length > 0 && files.every((f) => !f.webkitRelativePath)) {
    return {
      ok: false,
      error: '当前浏览器不支持读取文件夹结构，请改用「选择多份 XML」或桌面浏览器上传文件夹',
    }
  }
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
      entry = { label: name, files: [] }
      entries.set(key, entry)
    }
    entry.files.push(file)
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
 * 手机端兼容模式：平铺多选多份 XML 文件，按文件名各自作为一个条目并补上不含数字的虚拟文件夹路径。
 * 不支持按子目录合并多 P，重名文件会直接拦截提示先改名。
 */
export function groupFlatXmlFiles(
  files: readonly File[],
  folderName = MOBILE_UPLOAD_FOLDER,
): FolderGrouping {
  const sorted = files.toSorted((a, b) => naturalOrder.compare(a.name, b.name))
  const entries: UploadEntry[] = []
  const labels = new Set<string>()
  let ignored = 0

  for (const file of sorted) {
    if (!file.name.toLowerCase().endsWith('.xml')) {
      ignored++
      continue
    }
    const label = stripExtension(file.name)
    if (labels.has(label)) {
      return { ok: false, error: `条目名称重复：${label}（请先为重名 XML 文件改名后再选）` }
    }
    labels.add(label)
    entries.push({
      label,
      files: [withRelativePath(file, `${folderName}/${file.name}`)],
    })
  }
  if (entries.length === 0) {
    return { ok: false, error: '所选文件里没有 XML 文件' }
  }
  return {
    ok: true,
    folder: folderName,
    entries: entries.toSorted((a, b) => naturalOrder.compare(a.label, b.label)),
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
  return bytesToHex(digest)
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
