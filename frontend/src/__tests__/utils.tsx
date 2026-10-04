import { vi } from 'vitest'
import { act, render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router'

import { ApiError } from '@/api/request'
import { getSyncRun, listSyncRuns, type SyncRunDetail } from '@/api/sync'
import { routes } from '@/router/routes'

/** 在 path 渲染完整的路由（根布局加页面），每次用新的 QueryClient */
export function renderRoutes(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const router = createMemoryRouter(routes, { initialEntries: [path] })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { queryClient, router }
}

/** 一次同步记录，默认是成功结束、没有警告的手动同步 */
export function syncRun(id: number, patch: Partial<SyncRunDetail> = {}): SyncRunDetail {
  return {
    id,
    trigger: 'manual',
    status: 'succeeded',
    startedAt: '2026-10-05T08:00:00Z',
    finishedAt: '2026-10-05T08:01:30Z',
    total: 5,
    done: 5,
    createdSeries: 0,
    createdSeasons: 0,
    createdEpisodes: 0,
    warningCount: 0,
    warnings: [],
    error: null,
    ...patch,
  }
}

/**
 * 假的同步记录接口，在 beforeEach 里调用；测试文件要先 vi.mock('@/api/sync')。
 * listSyncRuns、getSyncRun 按返回对象的 runs（服务端的同步记录，新的在前）返回，用例改 runs 来推进同步。
 */
export function mockSyncRuns() {
  const server = {
    runs: [] as SyncRunDetail[],
    /** 把最新的那次同步改成 patch 后的样子 */
    updateLatest(patch: Partial<SyncRunDetail>) {
      server.runs[0] = { ...server.runs[0]!, ...patch }
    },
  }
  vi.mocked(listSyncRuns).mockImplementation(async () => structuredClone(server.runs))
  vi.mocked(getSyncRun).mockImplementation(async (id) => {
    const run = server.runs.find((r) => r.id === id)
    if (!run) throw new ApiError('同步记录不存在', 1, 404)
    return structuredClone(run)
  })
  return server
}

/** 推进假时间（vi.useFakeTimers），期间到点的轮询照常触发 */
export const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms))
