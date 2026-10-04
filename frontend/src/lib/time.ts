/**
 * 把秒数写成"1 小时 30 分钟"，与配置里时长的写法（24h、1h30m）对应，不折算成天；不足 1 秒的部分保留小数。
 */
export function formatSeconds(seconds: number) {
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = seconds % 60
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
