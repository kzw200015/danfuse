import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

import { advance, mockSyncRuns, syncRun } from '@/__tests__/utils'
import { getSyncRun } from '@/api/sync'
import { seriesKeys } from '@/hooks/use-series'
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
  queryClient.setQueryData(seriesKeys.list, [])
  queryClient.setQueryData(seriesKeys.detail(7), {})
  const { result } = renderHook(useLatestSyncRun, {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  })
  const seriesInvalidated = () =>
    [seriesKeys.list, seriesKeys.detail(7)].map(
      (key) => queryClient.getQueryState(key)?.isInvalidated,
    )
  return { result, seriesInvalidated }
}

describe('useLatestSyncRun', () => {
  it('最近一次同步为 running 时每秒轮询它的详情，结束后停止并让剧列表和剧详情失效', async () => {
    server.runs = [
      syncRun(2, { status: 'running', finishedAt: null, total: 3, done: 1 }),
      syncRun(1),
    ]
    const { result, seriesInvalidated } = renderLatestSyncRun()
    await waitFor(() => expect(getSyncRun).toHaveBeenCalledWith(2))

    // 每秒一次：推进 3 秒至少新增 3 次请求
    const calls = vi.mocked(getSyncRun).mock.calls.length
    server.updateLatest({ done: 2 })
    await advance(3000)
    expect(vi.mocked(getSyncRun).mock.calls.length - calls).toBeGreaterThanOrEqual(3)
    // 详情顺带替换列表里的这一条
    await waitFor(() => expect(result.current).toMatchObject({ id: 2, done: 2 }))
    expect(seriesInvalidated()).toEqual([false, false])

    server.updateLatest({ status: 'succeeded', done: 3, finishedAt: '2026-10-05T08:03:00Z' })
    await advance(1000)
    await waitFor(() => expect(seriesInvalidated()).toEqual([true, true]))
    expect(result.current).toMatchObject({ id: 2, status: 'succeeded' })

    const finished = vi.mocked(getSyncRun).mock.calls.length
    await advance(5000)
    expect(getSyncRun).toHaveBeenCalledTimes(finished)
  })

  it('最近一次同步已经结束时不轮询，也不让剧列表失效', async () => {
    server.runs = [syncRun(1, { warningCount: 3 })]
    const { result, seriesInvalidated } = renderLatestSyncRun()

    await waitFor(() => expect(result.current).toMatchObject({ id: 1, warningCount: 3 }))
    await advance(5000)
    expect(getSyncRun).not.toHaveBeenCalled()
    expect(seriesInvalidated()).toEqual([false, false])
  })
})
