import { describe, expect, it } from 'vitest'

import { syncRun } from '@/__tests__/utils'
import { progressValue } from '@/lib/sync'

describe('progressValue', () => {
  it.each([
    ['还在列出媒体库：进度不确定', syncRun(1, { status: 'running', total: null, done: 0 }), null],
    ['没列出媒体库就结束了', syncRun(1, { status: 'failed', total: null, done: 0 }), 0],
    ['媒体库是空的，成功结束', syncRun(1, { status: 'succeeded', total: 0, done: 0 }), 100],
    ['媒体库是空的，被打断', syncRun(1, { status: 'interrupted', total: 0, done: 0 }), 0],
    ['按已完成/总数', syncRun(1, { status: 'running', total: 8, done: 2 }), 25],
    ['全部完成', syncRun(1, { total: 5, done: 5 }), 100],
  ])('%s', (_, run, want) => {
    expect(progressValue(run)).toBe(want)
  })
})
