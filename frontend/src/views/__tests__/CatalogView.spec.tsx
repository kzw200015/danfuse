import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'

import { renderRoutes } from '@/__tests__/utils'
import { ApiError } from '@/api/request'
import { getSeries, listSeries, type SeriesDetail } from '@/api/series'
import { getSettings } from '@/api/settings'
import { listSyncRuns } from '@/api/sync'
import { seriesKeys } from '@/hooks/use-series'

vi.mock('@/api/series')
// 根布局会取同步列表和设置
vi.mock('@/api/settings')
vi.mock('@/api/sync')

const tv: SeriesDetail = {
  id: 1,
  type: 'tv',
  title: '星海旅人',
  originalTitle: 'Star Voyager',
  year: 2019,
  posterImageId: 5,
  seasons: [
    {
      id: 10,
      number: 0,
      title: null,
      episodes: [{ id: 100, number: 1, title: '番外', duration: 600 }],
    },
    {
      id: 11,
      number: 1,
      title: '远航篇',
      episodes: [
        { id: 110, number: 1, title: '启程', duration: 1420 },
        { id: 111, number: 2, title: null, duration: null },
      ],
    },
  ],
}

const movie: SeriesDetail = {
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
      episodes: [{ id: 200, number: 1, title: null, duration: 5400 }],
    },
  ],
}

beforeEach(() => {
  vi.mocked(getSettings).mockResolvedValue({ catalogSource: null, syncInterval: 0 })
  vi.mocked(listSyncRuns).mockResolvedValue([])
  const all = [tv, movie]
  vi.mocked(listSeries).mockResolvedValue(
    all.map(({ seasons, ...s }) => ({
      ...s,
      seasonCount: seasons.length,
      episodeCount: seasons.reduce((n, se) => n + se.episodes.length, 0),
    })),
  )
  vi.mocked(getSeries).mockImplementation(async (id) => {
    const series = all.find((s) => s.id === id)
    if (!series) throw new ApiError('剧不存在', 1, 404)
    return series
  })
})

const seasonNav = () => within(screen.getByRole('navigation', { name: '季' }))

describe('CatalogView', () => {
  it('剧：默认选中第 1 季，右栏显示季面板', async () => {
    renderRoutes('/catalog/1')

    expect(await screen.findByRole('heading', { name: '星海旅人' })).toBeInTheDocument()
    expect(screen.getByText('Star Voyager')).toBeInTheDocument()
    expect(
      within(screen.getByRole('complementary')).getByRole('link', { name: /星海旅人/ }),
    ).toHaveAttribute('aria-current', 'true')

    expect(seasonNav().getByRole('link', { name: '特别篇' })).not.toHaveAttribute('aria-current')
    expect(seasonNav().getByRole('link', { name: 'S1' })).toHaveAttribute('aria-current', 'true')
    expect(screen.getByRole('link', { name: /启程/ })).toHaveAttribute('href', '/catalog/1/11/110')
    expect(screen.getByRole('link', { name: /23:40/ })).toBeInTheDocument()

    expect(screen.getByTitle('查看整季')).toHaveAttribute('aria-current', 'true')
    expect(screen.getByRole('heading', { name: /^第 1 季/ })).toHaveTextContent('第 1 季远航篇')
    expect(screen.getByText('2 集 · 季 ID 11')).toBeInTheDocument()
  })

  it('点集打开集面板，点季标题行回到季面板', async () => {
    const { router } = renderRoutes('/catalog/1/10')

    expect(await screen.findByRole('heading', { name: '第 0 季（特别篇）' })).toBeInTheDocument()
    expect(seasonNav().getByRole('link', { name: '特别篇' })).toHaveAttribute(
      'aria-current',
      'true',
    )

    fireEvent.click(seasonNav().getByRole('link', { name: 'S1' }))
    fireEvent.click(await screen.findByRole('link', { name: /启程/ }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/catalog/1/11/110'))
    expect(screen.getByTitle('查看整季')).not.toHaveAttribute('aria-current')
    expect(screen.getByText('星海旅人 › 第 1 季')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: '第 1 集启程' })).toBeInTheDocument()
    expect(screen.getByText('时长 23:40 · 集 ID 110')).toBeInTheDocument()

    fireEvent.click(screen.getByTitle('查看整季'))

    await waitFor(() => expect(router.state.location.pathname).toBe('/catalog/1/11'))
    expect(screen.getByText('2 集 · 季 ID 11')).toBeInTheDocument()
  })

  it('电影：不显示季切换和集列表，右栏直接显示正片', async () => {
    renderRoutes('/catalog/2')

    expect(await screen.findByRole('heading', { name: '正片' })).toBeInTheDocument()
    expect(screen.getByText('时长 1:30:00 · 集 ID 200')).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: '季' })).not.toBeInTheDocument()
    expect(screen.queryByTitle('查看整季')).not.toBeInTheDocument()
  })

  it('有海报时显示图片，没有海报或加载失败时显示占位图', async () => {
    renderRoutes('/catalog/1')
    expect(await screen.findByRole('heading', { name: '星海旅人' })).toBeInTheDocument()

    // 左栏这一行和中栏各一张；电影没有海报
    const posters = screen.getAllByRole('presentation')
    expect(posters).toHaveLength(2)
    for (const img of posters) {
      expect(img).toHaveAttribute('src', '/api/images/5')
    }
    const list = within(screen.getByRole('complementary'))
    expect(
      within(list.getByRole('link', { name: /长夜灯塔/ })).getByTitle('没有海报'),
    ).toBeVisible()

    fireEvent.error(posters[1]!)

    expect(screen.getAllByRole('presentation')).toHaveLength(1)
    expect(screen.getAllByTitle('没有海报')).toHaveLength(2)
  })

  it('筛选剧列表', async () => {
    renderRoutes('/catalog')
    const list = within(await screen.findByRole('complementary'))
    expect(await list.findByText('2 部')).toBeInTheDocument()

    fireEvent.change(list.getByRole('textbox', { name: '筛选剧名或原名' }), {
      target: { value: 'voyager' },
    })

    expect(list.getByText('1 部')).toBeInTheDocument()
    expect(list.getByRole('link', { name: /星海旅人/ })).toBeInTheDocument()
    expect(list.queryByRole('link', { name: /长夜灯塔/ })).not.toBeInTheDocument()
  })

  it.each([
    ['/catalog/3', '找不到这部剧', '返回目录', '/catalog'],
    ['/catalog/abc', '找不到这部剧', '返回目录', '/catalog'],
    ['/catalog/1/20', '找不到这一季', '返回星海旅人', '/catalog/1'],
    ['/catalog/1/11/100', '找不到这一集', '返回第 1 季', '/catalog/1/11'],
    // 电影在界面上没有季，返回这部电影
    ['/catalog/2/20/999', '找不到这一集', '返回长夜灯塔', '/catalog/2'],
  ])('%s 不存在时提示并返回上一级', async (path, text, back, backTo) => {
    renderRoutes(path)

    expect(await screen.findByText(new RegExp(text))).toBeInTheDocument()
    expect(screen.getByRole('link', { name: back })).toHaveAttribute('href', backTo)
  })

  it('已加载的剧重新加载时不存在了，显示找不到', async () => {
    const { queryClient } = renderRoutes('/catalog/1')
    expect(await screen.findByRole('heading', { name: '星海旅人' })).toBeInTheDocument()

    vi.mocked(getSeries).mockRejectedValue(new ApiError('剧不存在', 1, 404))
    await act(() => queryClient.invalidateQueries({ queryKey: seriesKeys.list }))

    expect(await screen.findByText(/找不到这部剧/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '返回目录' })).toHaveAttribute('href', '/catalog')
    expect(screen.queryByRole('heading', { name: '星海旅人' })).not.toBeInTheDocument()
  })
})
