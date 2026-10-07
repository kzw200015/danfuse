import type { FollowSettings } from '@/api/settings'
import { formatSeconds } from '@/lib/time'

/** 追更开关的说明，例如"每 12 小时检查一次合集、目录同步进来新的集后 1 分钟内补建" */
export function followRuleText(follow: FollowSettings) {
  return `${followCostText(follow)}、目录同步进来新的集后 ${formatSeconds(follow.scanInterval)}内补建`
}

/** 开着追更的代价：定期检查合集 */
export function followCostText(follow: FollowSettings) {
  return `每 ${formatSeconds(follow.checkInterval)}检查一次合集`
}
