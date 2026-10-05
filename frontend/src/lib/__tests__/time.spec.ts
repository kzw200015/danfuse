import { describe, expect, it } from 'vitest'

import { formatDuration, formatSeconds } from '@/lib/time'

describe('formatSeconds', () => {
  it.each([
    [86400, '24 小时'],
    [5400, '1 小时 30 分钟'],
    [3601, '1 小时 1 秒'],
    [90, '1 分钟 30 秒'],
    [90.3, '1 分钟 30.3 秒'],
    [1.5, '1.5 秒'],
    [0, '0 秒'],
  ])('%s 秒 → %s', (seconds, want) => {
    expect(formatSeconds(seconds)).toBe(want)
  })
})

describe('formatDuration', () => {
  it.each([
    [null, '—'],
    [0, '0:00'],
    [45, '0:45'],
    [1420, '23:40'],
    [3600, '1:00:00'],
    [5405, '1:30:05'],
  ])('%j 秒', (seconds, want) => {
    expect(formatDuration(seconds)).toBe(want)
  })
})
