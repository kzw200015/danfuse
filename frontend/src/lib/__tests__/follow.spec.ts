import { describe, expect, it } from 'vitest'

import { settings } from '@/__tests__/utils'
import { followCostText, followRuleText } from '@/lib/follow'

const defaults = settings().follow

describe('followRuleText', () => {
  it.each([
    [
      '默认值',
      defaults,
      '每 12 小时检查一次合集、目录同步进来新的集后 1 分钟内补建，并在绑定建出后 336 小时内每 12 小时重新拉取一次',
    ],
    [
      '不自动重新拉取',
      { ...defaults, refetchWindow: 0 },
      '每 12 小时检查一次合集、目录同步进来新的集后 1 分钟内补建',
    ],
  ])('%s', (_, follow, want) => {
    expect(followRuleText(follow)).toBe(want)
  })
})

describe('followCostText', () => {
  it.each([
    ['自动重新拉取', defaults, '建出的绑定在 336 小时内每 12 小时自动重新拉取一次'],
    ['不自动重新拉取', { ...defaults, refetchWindow: 0 }, '每 12 小时检查一次合集'],
  ])('%s', (_, follow, want) => {
    expect(followCostText(follow)).toBe(want)
  })
})
