import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { advance, mockSyncRuns, seedSeries, syncRun } from '@/__tests__/utils'
import { getLatestSyncRun } from '@/api/sync'
import { useLatestSyncRun } from '@/hooks/use-sync-runs'

vi.mock('@/api/sync')

let server: ReturnType<typeof mockSyncRuns>

beforeEach(() => {
  // 轮询的定时器用假时间推进；shouldAdvanceTime 让其他定时器（查询的通知批处理、waitFor）照常走
  vi.useFakeTimers({ shouldAdvanceTime: true })
  server = mockSyncRuns()
})

afterEach(() => {
  vi.useRealTimers()
  vi.resetAllMocks()
})

/** 用新的 QueryClient 渲染 useLatestSyncRun，并放进目录页的查询：剧列表和打开的那部剧 */
function renderLatestSyncRun() {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const seriesInvalidated = seedSeries(queryClient)
  const { result } = renderHook(useLatestSyncRun, {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  })
  return { result, seriesInvalidated }
}

describe('useLatestSyncRun', () => {
  it('一直每 2 秒轮询最近一次同步，running 的结束后让剧列表和剧详情失效', async () => {
    server.runs = [
      syncRun(2, { status: 'running', finishedAt: null, total: 3, done: 1 }),
      syncRun(1),
    ]
    const { result, seriesInvalidated } = renderLatestSyncRun()
    await waitFor(() => expect(result.current).toMatchObject({ id: 2, done: 1 }))

    // 每 2 秒一次：推进 6 秒至少新增 3 次请求
    const calls = vi.mocked(getLatestSyncRun).mock.calls.length
    server.updateLatest({ done: 2 })
    await advance(6000)
    expect(vi.mocked(getLatestSyncRun).mock.calls.length - calls).toBeGreaterThanOrEqual(3)
    await waitFor(() => expect(result.current).toMatchObject({ id: 2, done: 2 }))
    expect(seriesInvalidated()).toEqual([false, false])

    server.updateLatest({ status: 'succeeded', done: 3, finishedAt: '2026-10-05T08:03:00Z' })
    await advance(2000)
    await waitFor(() => expect(seriesInvalidated()).toEqual([true, true]))
    expect(result.current).toMatchObject({ id: 2, status: 'succeeded' })

    // 结束之后照常轮询
    const finished = vi.mocked(getLatestSyncRun).mock.calls.length
    await advance(4000)
    expect(vi.mocked(getLatestSyncRun).mock.calls.length - finished).toBeGreaterThanOrEqual(2)
  })

  it('最近一次同步已经结束时不让剧列表失效', async () => {
    server.runs = [syncRun(1, { warningCount: 3 })]
    const { result, seriesInvalidated } = renderLatestSyncRun()

    await waitFor(() => expect(result.current).toMatchObject({ id: 1, warningCount: 3 }))
    await advance(5000)
    expect(seriesInvalidated()).toEqual([false, false])
  })

  it('一次都没同步过时为 undefined；之后出现的同步没赶上轮询就已结束，也让剧列表失效', async () => {
    const { result, seriesInvalidated } = renderLatestSyncRun()
    await waitFor(() => expect(getLatestSyncRun).toHaveBeenCalled())
    expect(result.current).toBeUndefined()

    server.runs = [syncRun(1, { createdSeries: 1 })]
    await advance(2000)
    await waitFor(() => expect(seriesInvalidated()).toEqual([true, true]))
    expect(result.current).toMatchObject({ id: 1 })
  })
})
