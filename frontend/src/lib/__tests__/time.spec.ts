import { describe, expect, it } from 'vitest'

import { formatSeconds } from '@/lib/time'

describe('formatSeconds', () => {
  it.each([
    [86400, '24 小时'],
    [5400, '1 小时 30 分钟'],
    [3601, '1 小时 1 秒'],
    [90, '1 分钟 30 秒'],
    [1.5, '1.5 秒'],
    [0, '0 秒'],
  ])('%s 秒 → %s', (seconds, want) => {
    expect(formatSeconds(seconds)).toBe(want)
  })
})
