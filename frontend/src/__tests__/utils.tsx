import { render } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router'

import type { SyncRunDetail } from '@/api/sync'
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
