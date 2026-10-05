/**
 * 把秒数写成"1 小时 30 分钟"，与配置里时长的写法（24h、1h30m）对应，不折算成天；不足 1 秒的部分保留小数。
 * 按整数毫秒拆分，免得 90.3 % 60 这类浮点取余写出 30.299999999999997。
 */
export function formatSeconds(seconds: number) {
  const ms = Math.round(seconds * 1000)
  const h = Math.floor(ms / 3_600_000)
  const m = Math.floor((ms % 3_600_000) / 60_000)
  const s = (ms % 60_000) / 1000
  const parts = [h > 0 && `${h} 小时`, m > 0 && `${m} 分钟`, s > 0 && `${s} 秒`]
  return parts.filter(Boolean).join(' ') || '0 秒'
}

/** 本地时间，例如 2026/10/5 08:30:00 */
export function formatDateTime(iso: string) {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

/** 距今多久，例如"3 分钟前" */
export function formatAgo(iso: string) {
  const seconds = (Date.now() - Date.parse(iso)) / 1000
  if (seconds < 60) return '刚刚'
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
  return `${Math.floor(seconds / 86400)} 天前`
}

/** 两个时间点相隔的秒数，四舍五入到整秒 */
export function secondsBetween(from: string, to: string) {
  return Math.round((Date.parse(to) - Date.parse(from)) / 1000)
}

/** 时长（秒）显示为 m:ss 或 h:mm:ss，没有时显示"—" */
export function formatDuration(seconds: number | null) {
  if (seconds === null) return '—'
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const ss = String(seconds % 60).padStart(2, '0')
  return h ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`
}
