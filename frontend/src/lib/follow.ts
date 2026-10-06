import type { FollowSettings } from '@/api/settings'
import { formatSeconds } from '@/lib/time'

/** 追更开关的说明，例如"每 12 小时检查一次合集、目录同步进来新的集后 1 分钟内补建，并在绑定建出后 336 小时内每 12 小时重新拉取一次" */
export function followRuleText(follow: FollowSettings) {
  const check = formatSeconds(follow.checkInterval)
  const text = `每 ${check}检查一次合集、目录同步进来新的集后 ${formatSeconds(follow.scanInterval)}内补建`
  if (follow.refetchWindow === 0) return text
  return `${text}，并在绑定建出后 ${formatSeconds(follow.refetchWindow)}内每 ${check}重新拉取一次`
}

/** 开着追更的代价：自动重新拉取关闭时只剩定期检查合集 */
export function followCostText(follow: FollowSettings) {
  const check = formatSeconds(follow.checkInterval)
  if (follow.refetchWindow === 0) return `每 ${check}检查一次合集`
  return `建出的绑定在 ${formatSeconds(follow.refetchWindow)}内每 ${check}自动重新拉取一次`
}
