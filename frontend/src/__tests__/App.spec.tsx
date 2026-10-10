import { beforeEach, describe, expect, it, onTestFinished, vi } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'

import { listBlockedWords } from '@/api/blocked-words'
import { listSeries } from '@/api/series'
import { getSettings } from '@/api/settings'
import { getLatestSyncRun, getSyncRun, listSyncRuns, type SyncRunDetail } from '@/api/sync'
import { mockRootLayout, renderRoutes, settings, syncRun } from './utils'

vi.mock('@/api/blocked-words')
vi.mock('@/api/series')
vi.mock('@/api/settings')
vi.mock('@/api/sync')

beforeEach(() => {
  vi.mocked(listBlockedWords).mockResolvedValue([])
  vi.mocked(listSeries).mockResolvedValue([])
  mockRootLayout()
  vi.mocked(listSyncRuns).mockResolvedValue([])
})

describe('App', () => {
  it('根路径重定向到目录页', async () => {
    const { router } = renderRoutes('/')

    expect(await screen.findByRole('link', { name: '目录' })).toHaveAttribute(
      'aria-current',
      'page',
    )
    expect(router.state.location.pathname).toBe('/catalog')
  })

  it('顶栏导航切换页面', async () => {
    const { router } = renderRoutes('/catalog')

    fireEvent.click(await screen.findByRole('link', { name: '同步' }))

    // 页面是懒加载的，加载完成后导航才生效
    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/sync')
      expect(screen.getByRole('link', { name: '同步' })).toHaveAttribute('aria-current', 'page')
      expect(screen.getByRole('link', { name: '目录' })).not.toHaveAttribute('aria-current')
    })
  })
})

describe('"同步"导航项上的同步状态', () => {
  it.each<{ name: string; latest: SyncRunDetail; want: string }>([
    {
      name: '还在列出媒体库',
      latest: syncRun(1, { status: 'running', finishedAt: null, total: null, done: 0 }),
      want: '同步…',
    },
    {
      name: '进行中显示已完成/总数',
      latest: syncRun(1, { status: 'running', finishedAt: null, total: 12, done: 5 }),
      want: '同步5/12',
    },
    {
      name: '失败',
      latest: syncRun(1, { status: 'failed', total: null, done: 0, error: '列出媒体库失败' }),
      want: '同步最近一次同步失败',
    },
    {
      name: '中断',
      latest: syncRun(1, { status: 'interrupted', finishedAt: null, done: 2 }),
      want: '同步最近一次同步已中断',
    },
    { name: '有警告显示条数', latest: syncRun(1, { warningCount: 4 }), want: '同步4' },
    { name: '正常结束不显示', latest: syncRun(1), want: '同步' },
  ])('$name', async ({ latest, want }) => {
    vi.mocked(getLatestSyncRun).mockResolvedValue(latest)
    vi.mocked(listSyncRuns).mockResolvedValue([latest, syncRun(0, { warningCount: 9 })])
    vi.mocked(getSyncRun).mockResolvedValue(latest)
    renderRoutes('/sync')

    // 同步页的表格出现时列表已经取到
    await screen.findByRole('cell', { name: '#1' })
    await waitFor(() =>
      expect(screen.getByRole('link', { name: /^同步/ })).toHaveAccessibleName(want),
    )
  })
})

async function openSettings() {
  fireEvent.click(await screen.findByRole('button', { name: '设置' }))
  return screen.findByRole('dialog')
}

/** 设置弹出层里 term 这一项的值 */
async function settingValue(dialog: HTMLElement, term: string) {
  return (await within(dialog).findByText(term)).nextElementSibling?.textContent
}

describe('设置弹出层', () => {
  it('只读显示配置，API key、SESSDATA 只显示已配置', async () => {
    vi.mocked(getSettings).mockResolvedValue(
      settings({
        catalogSource: {
          kind: 'jellyfin',
          url: 'http://192.168.1.10:8096',
          libraries: ['番剧', '电影'],
        },
        syncInterval: 86400,
        bilibiliSessdataConfigured: true,
      }),
    )
    renderRoutes('/catalog')

    const dialog = await openSettings()

    expect(await settingValue(dialog, '种类')).toBe('Jellyfin')
    expect(await settingValue(dialog, '地址')).toBe('http://192.168.1.10:8096')
    expect(await settingValue(dialog, '媒体库')).toBe('番剧电影')
    expect(await settingValue(dialog, '定时同步')).toBe('每 24 小时')
    expect(await settingValue(dialog, 'API key')).toBe('已配置')
    expect(await settingValue(dialog, '定时拉取')).toBe(
      '绑定建出后 336 小时内，每 12 小时重新拉取一次',
    )
    expect(await settingValue(dialog, 'SESSDATA')).toBe('已配置')
  })

  it('未配置目录源、SESSDATA，关闭定时拉取', async () => {
    vi.mocked(getSettings).mockResolvedValue(
      settings({ scheduledFetch: { interval: 43200, window: 0 } }),
    )
    renderRoutes('/catalog')

    const dialog = await openSettings()

    expect(await within(dialog).findByText('未配置目录源')).toBeInTheDocument()
    expect(await settingValue(dialog, '定时拉取')).toBe('关闭')
    expect(await settingValue(dialog, 'SESSDATA')).toBe('未配置，以未登录的身份拉取，弹幕可能不全')
  })

  it('弹弹 API 地址按当前页面的地址拼出，可以复制', async () => {
    vi.mocked(getSettings).mockResolvedValue(settings({ dandanplayToken: 's3cret' }))
    const writeText = vi.fn<(text: string) => Promise<void>>().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    onTestFinished(() => {
      Reflect.deleteProperty(navigator, 'clipboard')
    })
    renderRoutes('/catalog')

    const dialog = await openSettings()
    const url = `${location.origin}/dandanplay/s3cret`
    expect(await within(dialog).findByText(url)).toBeInTheDocument()

    fireEvent.click(within(dialog).getByRole('button', { name: '复制' }))
    expect(await screen.findByText('已复制弹弹 API 地址')).toBeInTheDocument()
    expect(writeText).toHaveBeenCalledWith(url)
  })

  it('浏览器不允许写剪贴板时提示手动复制', async () => {
    // 与通过 http 访问内网地址时一样，jsdom 没有 navigator.clipboard
    renderRoutes('/catalog')

    const dialog = await openSettings()
    expect(await within(dialog).findByText(`${location.origin}/dandanplay`)).toBeInTheDocument()

    fireEvent.click(within(dialog).getByRole('button', { name: '复制' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('手动复制')
  })
})
