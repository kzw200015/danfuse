import { describe, expect, it } from 'vitest'

import { settings } from '@/__tests__/utils'
import { followRuleText } from '@/lib/follow'

const defaults = settings().follow

describe('followRuleText', () => {
  it('默认值', () => {
    expect(followRuleText(defaults)).toBe(
      '每 12 小时检查一次合集、目录同步进来新的集后 1 分钟内补建',
    )
  })
})
