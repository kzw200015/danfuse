import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'

import {
  advance,
  mockSyncRuns,
  renderRoutes,
  seedSeries,
  settings,
  syncRun,
} from '@/__tests__/utils'
import { ApiError } from '@/api/request'
import { getSettings } from '@/api/settings'
import { getSyncRun, triggerSync } from '@/api/sync'

vi.mock('@/api/settings')
vi.mock('@/api/sync')

const jellyfin = settings({
  catalogSource: { kind: 'jellyfin', url: 'http://192.168.1.10:8096', libraries: ['番剧'] },
  syncInterval: 86400,
})

let server: ReturnType<typeof mockSyncRuns>

beforeEach(() => {
  // 轮询的定时器用假时间推进；shouldAdvanceTime 让其他定时器（渲染、toast、findBy）照常走
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.mocked(getSettings).mockResolvedValue(jellyfin)
  server = mockSyncRuns()
})

afterEach(() => {
  vi.useRealTimers()
  vi.resetAllMocks()
})

const syncNav = () => screen.findByRole('link', { name: /^同步/ })

/** 等同步列表取到（表格出现）后再点"立即同步"，免得列表的第一次请求晚于触发、直接取到这次同步 */
async function clickSyncNow() {
  await screen.findByRole('table')
  fireEvent.click(screen.getByRole('button', { name: '立即同步' }))
}

describe('标题行与立即同步', () => {
  it('触发后提示，顶栏和表格显示新的同步，结束后让剧列表失效', async () => {
    server.runs = [syncRun(1)]
    vi.mocked(triggerSync).mockImplementation(async () => {
      server.runs.unshift(syncRun(2, { status: 'running', finishedAt: null, total: null, done: 0 }))
      return { id: 2 }
    })
    const { queryClient } = renderRoutes('/sync')
    const invalidated = seedSeries(queryClient)

    await clickSyncNow()

    expect(await screen.findByText('已开始同步 #2')).toBeInTheDocument()
    // 还在列出媒体库
    expect(await within(await syncNav()).findByText('…')).toBeInTheDocument()
    expect(await screen.findByRole('cell', { name: '#2' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '同步中…' })).toBeDisabled()

    server.updateLatest({ total: 2, done: 1 })
    await advance(2000)
    expect(await within(await syncNav()).findByText('1/2')).toBeInTheDocument()
    expect(invalidated()).toEqual([false, false])

    server.updateLatest({
      status: 'succeeded',
      done: 2,
      createdSeries: 2,
      finishedAt: '2026-10-05T08:02:00Z',
    })
    await advance(2000)
    await waitFor(() => expect(invalidated()).toEqual([true, true]))
    expect(await screen.findByRole('button', { name: '立即同步' })).toBeEnabled()
  })

  it('已有同步在跑（还没轮询到，例如其他实例刚开始的）时提示，轮询到之后"立即同步"禁用', async () => {
    server.runs = [syncRun(1)]
    vi.mocked(triggerSync).mockRejectedValue(new ApiError('同步正在进行', 1, 409))
    renderRoutes('/sync')

    await clickSyncNow()
    expect(await screen.findByRole('alert')).toHaveTextContent('同步正在进行')

    server.runs.unshift(syncRun(2, { status: 'running', finishedAt: null, total: 3, done: 1 }))
    await advance(2000)
    expect(await screen.findByRole('button', { name: '同步中…' })).toBeDisabled()
    expect(await screen.findByRole('cell', { name: '#2' })).toBeInTheDocument()
  })

  it('触发后同步很快就结束、没赶上轮询时也让剧列表失效', async () => {
    server.runs = [syncRun(1)]
    vi.mocked(triggerSync).mockImplementation(async () => {
      server.runs.unshift(syncRun(2, { createdSeries: 1 }))
      return { id: 2 }
    })
    const { queryClient } = renderRoutes('/sync')
    const invalidated = seedSeries(queryClient)

    await clickSyncNow()

    await waitFor(() => expect(invalidated()).toEqual([true, true]))
  })

  it('页面打开之后才开始的同步（定时或其他实例）由轮询发现，"立即同步"随之禁用', async () => {
    server.runs = [syncRun(1)]
    renderRoutes('/sync')
    expect(await screen.findByRole('button', { name: '立即同步' })).toBeEnabled()

    server.runs.unshift(
      syncRun(2, { trigger: 'schedule', status: 'running', finishedAt: null, total: 3, done: 1 }),
    )
    await advance(2000)
    expect(await screen.findByRole('button', { name: '同步中…' })).toBeDisabled()
    expect(await within(await syncNav()).findByText('1/3')).toBeInTheDocument()
  })

  it('选中的同步还在运行时轮询详情，结束后停止', async () => {
    server.runs = [syncRun(1, { status: 'running', finishedAt: null, total: 3, done: 1 })]
    renderRoutes('/sync')
    expect(await screen.findByText('同步 #1')).toBeInTheDocument()

    server.updateLatest({ warningCount: 1, warnings: ['乙：没有有效的集，整部跳过'] })
    await advance(2000)
    expect(await screen.findByText('乙：没有有效的集，整部跳过')).toBeInTheDocument()

    server.updateLatest({ status: 'succeeded', done: 3, finishedAt: '2026-10-05T08:02:00Z' })
    await advance(2000)
    await waitFor(() => expect(screen.getByRole('button', { name: '立即同步' })).toBeEnabled())
    await advance(2000)
    const calls = vi.mocked(getSyncRun).mock.calls.length
    await advance(6000)
    expect(getSyncRun).toHaveBeenCalledTimes(calls)
  })

  it('其他失败显示在按钮下方，可以关闭', async () => {
    vi.mocked(triggerSync).mockRejectedValue(new ApiError('服务正在关闭', 1, 503))
    renderRoutes('/sync')

    await clickSyncNow()

    expect(await screen.findByRole('alert')).toHaveTextContent('服务正在关闭')
    fireEvent.click(screen.getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
  })

  it('未配置目录源时提示，"立即同步"禁用', async () => {
    vi.mocked(getSettings).mockResolvedValue(settings())
    renderRoutes('/sync')

    expect(await screen.findByText('未配置目录源')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '立即同步' })).toBeDisabled()
  })

  it('标题旁显示定时间隔和最近一次同步的开始时间', async () => {
    vi.setSystemTime(new Date('2026-10-05T11:00:00Z'))
    server.runs = [syncRun(1)]
    renderRoutes('/sync')

    expect(
      await screen.findByText('每 24 小时自动同步一次，最近一次开始于 3 小时前'),
    ).toBeInTheDocument()
  })
})

describe('同步详情', () => {
  it('默认显示最近一次，点表格里的一行用 ?run 选中', async () => {
    server.runs = [
      syncRun(2, { createdSeries: 1, createdSeasons: 2, createdEpisodes: 12 }),
      syncRun(1, {
        trigger: 'schedule',
        status: 'failed',
        total: null,
        done: 0,
        error: '列出媒体库失败：GET /Library/VirtualFolders: 401 Unauthorized',
      }),
    ]
    const { router } = renderRoutes('/sync')

    expect(await screen.findByText('同步 #2')).toBeInTheDocument()
    expect(screen.getByText('新增 剧 1 · 季 2 · 集 12')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('cell', { name: '#1' }))

    expect(await screen.findByText('同步 #1')).toBeInTheDocument()
    expect(router.state.location.search).toBe('?run=1')
    expect(screen.getByRole('alert')).toHaveTextContent(
      '失败原因：列出媒体库失败：GET /Library/VirtualFolders: 401 Unauthorized',
    )
  })

  it('中断的同步说明已提交的部分保留', async () => {
    server.runs = [syncRun(1, { status: 'interrupted', finishedAt: null, total: 5, done: 2 })]
    renderRoutes('/sync?run=1')

    expect(await screen.findByText(/已提交的部分保留，重新同步即可补齐/)).toBeInTheDocument()
  })

  it('警告超过保存的条数时注明总数', async () => {
    const warnings = Array.from({ length: 200 }, (_, i) => `剧${i}：没有有效的集，整部跳过`)
    server.runs = [syncRun(1, { warningCount: 250, warnings })]
    renderRoutes('/sync?run=1')

    expect(await screen.findByText('共 250 条，只保存了前 200 条')).toBeInTheDocument()
    const list = screen.getByRole('list', { name: '警告' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(200)
  })

  it('选中的同步不存在时显示原因', async () => {
    server.runs = [syncRun(1)]
    renderRoutes('/sync?run=9')

    expect(await screen.findByRole('alert')).toHaveTextContent('同步记录不存在')
  })

  it('?run= 不是合法的 ID 时不发请求，直接显示不存在', async () => {
    server.runs = [syncRun(1)]
    renderRoutes('/sync?run=abc')

    expect(await screen.findByRole('alert')).toHaveTextContent('同步记录不存在')
    expect(await screen.findByRole('cell', { name: '#1' })).toBeInTheDocument()
    expect(getSyncRun).not.toHaveBeenCalled()
  })
})
