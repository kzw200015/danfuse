import { vi } from 'vitest'
import { act, render, screen, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router'

import type { FileBinding, LinkBinding } from '@/api/bindings'
import { ApiError } from '@/api/request'
import { getSeries, listSeries, type SeriesDetail, type SeriesSummary } from '@/api/series'
import { getSettings, type Settings } from '@/api/settings'
import { getLatestSyncRun, getSyncRun, listSyncRuns, type SyncRunDetail } from '@/api/sync'
import { seriesKeys } from '@/hooks/use-series'
import { routes } from '@/router/routes'

/** 每个用例新建的 QueryClient，与应用的一样失败不重试 */
export function newQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

/** 在 path 渲染完整的路由（根布局加页面），每次用新的 QueryClient */
export function renderRoutes(path: string) {
  const queryClient = newQueryClient()
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
 * listSyncRuns、getSyncRun、getLatestSyncRun 按返回对象的 runs（服务端的同步记录，新的在前）返回，用例改 runs 来推进同步。
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
  vi.mocked(getLatestSyncRun).mockImplementation(async () =>
    server.runs[0] ? structuredClone(server.runs[0]) : null,
  )
  vi.mocked(getSyncRun).mockImplementation(async (id) => {
    const run = server.runs.find((r) => r.id === id)
    if (!run) throw new ApiError('同步记录不存在', 1, 404)
    return structuredClone(run)
  })
  return server
}

/** 推进假时间（vi.useFakeTimers），期间到点的轮询照常触发 */
export const advance = (ms: number) => act(() => vi.advanceTimersByTimeAsync(ms))

/** 配置，默认是什么都没配置 */
export function settings(patch: Partial<Settings> = {}): Settings {
  return {
    dandanplayToken: null,
    catalogSource: null,
    syncInterval: 0,
    bilibiliSessdataConfigured: false,
    follow: { scanInterval: 60, checkInterval: 43200 },
    scheduledFetch: { interval: 43200, window: 1209600 },
    ...patch,
  }
}

/**
 * 根布局会取最近一次同步和设置：设置为默认的什么都没配置，一次都没同步过。
 * 在 beforeEach 里调用；测试文件要先 vi.mock('@/api/settings') 和 vi.mock('@/api/sync')。
 */
export function mockRootLayout() {
  vi.mocked(getSettings).mockResolvedValue(settings())
  vi.mocked(getLatestSyncRun).mockResolvedValue(null)
}

/** 一个绑定，默认是正常的 B 站投稿 */
export function binding(id: number, patch: Partial<LinkBinding> = {}): LinkBinding {
  return {
    id,
    kind: 'link',
    adapter: 'bilibili',
    sourceUrl: `https://www.bilibili.com/video/BV1xx411c7X${id}`,
    sourceLabel: `B 站投稿 BV1xx411c7X${id}`,
    title: `弹幕源 ${id}`,
    duration: 1420,
    offset: 0,
    status: 'active',
    danmakuCount: 1234,
    contentVersion: 1,
    maxTimeMs: 1_420_500,
    lastFetchedAt: '2026-10-05T08:00:00Z',
    seasonBindingId: null,
    ...patch,
  }
}

/** 一个用弹幕文件建的绑定：没有适配器、链接和时长 */
export function fileBinding(id: number, patch: Partial<FileBinding> = {}): FileBinding {
  return {
    id,
    kind: 'file',
    adapter: null,
    sourceUrl: null,
    sourceLabel: '弹幕文件 · 2 份',
    title: '20130709',
    duration: null,
    offset: 0,
    status: 'active',
    danmakuCount: 3000,
    contentVersion: 1,
    maxTimeMs: 1_420_500,
    lastFetchedAt: null,
    seasonBindingId: null,
    ...patch,
  }
}

/** 电影「长夜灯塔」（剧 2）：唯一的一集（集 200）没有绑定 */
export function lighthouse(): SeriesDetail {
  return {
    id: 2,
    type: 'movie',
    title: '长夜灯塔',
    originalTitle: null,
    year: 2020,
    posterImageId: null,
    seasons: [
      {
        id: 20,
        number: 1,
        title: null,
        seasonBindings: [],
        episodes: [{ id: 200, number: 1, title: null, duration: 5400, bindings: [] }],
      },
    ],
  }
}

/** 剧列表的一项，统计由剧详情现算 */
function summarize({ seasons, ...series }: SeriesDetail): SeriesSummary {
  const episodes = seasons.flatMap((se) => se.episodes)
  const bindings = episodes.flatMap((e) => e.bindings)
  return {
    ...series,
    seasonCount: seasons.length,
    episodeCount: episodes.length,
    boundEpisodeCount: episodes.filter((e) => e.bindings.length > 0).length,
    bindingCount: bindings.length,
    deadBindingCount: bindings.filter((b) => b.status === 'dead').length,
    following: seasons.some((se) => se.seasonBindings.some((sb) => sb.follow)),
  }
}

/**
 * 假的目录接口，在 beforeEach 里调用；测试文件要先 vi.mock('@/api/series')。
 * listSeries、getSeries 每次按 all() 返回的剧（服务端的目录）现算，用例改目录来模拟后端的变化。
 */
export function mockCatalog(all: () => SeriesDetail[]) {
  vi.mocked(listSeries).mockImplementation(async () => all().map(summarize))
  vi.mocked(getSeries).mockImplementation(async (id) => {
    const series = all().find((s) => s.id === id)
    if (!series) throw new ApiError('剧不存在', 1, 404)
    return structuredClone(series)
  })
}

/** 预放目录页的查询（剧列表和打开的那部剧），返回一个函数，取它们是否已失效 */
export function seedSeries(queryClient: QueryClient) {
  queryClient.setQueryData(seriesKeys.list, [])
  queryClient.setQueryData(seriesKeys.detail(7), {})
  return () =>
    [seriesKeys.list, seriesKeys.detail(7)].map(
      (key) => queryClient.getQueryState(key)?.isInvalidated,
    )
}

/** 名称为 name 的卡片（绑定或季绑定） */
export const card = async (name: string) => within(await screen.findByRole('article', { name }))
