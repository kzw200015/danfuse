import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'

import { renderRoutes } from '@/__tests__/utils'
import {
  createBinding,
  deleteBinding,
  refetchBinding,
  updateBindingOffset,
  type Binding,
} from '@/api/bindings'
import { ApiError } from '@/api/request'
import { getSeries, listSeries, type SeriesDetail } from '@/api/series'
import { getSettings } from '@/api/settings'
import { listSyncRuns } from '@/api/sync'
import { seriesKeys } from '@/hooks/use-series'

vi.mock('@/api/bindings')
vi.mock('@/api/series')
// 根布局会取同步列表和设置
vi.mock('@/api/settings')
vi.mock('@/api/sync')

function binding(id: number, patch: Partial<Binding> = {}): Binding {
  return {
    id,
    adapter: 'bilibili',
    sourceUrl: `https://www.bilibili.com/video/BV1xx411c7X${id}`,
    sourceLabel: `B 站投稿 BV1xx411c7X${id}`,
    title: `弹幕源 ${id}`,
    duration: 1420,
    offset: 0,
    status: 'active',
    danmakuCount: 1234,
    lastFetchedAt: '2026-10-05T08:00:00Z',
    ...patch,
  }
}

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
      episodes: [{ id: 100, number: 1, title: '番外', duration: 600, bindings: [] }],
    },
    {
      id: 11,
      number: 1,
      title: '远航篇',
      episodes: [
        {
          id: 110,
          number: 1,
          title: '启程',
          duration: 1420,
          bindings: [
            binding(1, { duration: 1427 }),
            binding(2, { status: 'dead', duration: 1418, danmakuCount: 0 }),
          ],
        },
        { id: 111, number: 2, title: null, duration: null, bindings: [] },
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
      episodes: [{ id: 200, number: 1, title: null, duration: 5400, bindings: [] }],
    },
  ],
}

/** 服务端的目录，mock 的接口按它返回；用例改它来模拟新建的绑定 */
let all: SeriesDetail[]

beforeEach(() => {
  all = structuredClone([tv, movie])
  vi.mocked(getSettings).mockResolvedValue({
    dandanplayToken: null,
    catalogSource: null,
    syncInterval: 0,
    bilibiliSessdataConfigured: false,
  })
  vi.mocked(listSyncRuns).mockResolvedValue([])
  vi.mocked(listSeries).mockImplementation(async () =>
    all.map(({ seasons, ...s }) => {
      const episodes = seasons.flatMap((se) => se.episodes)
      const bindings = episodes.flatMap((e) => e.bindings)
      return {
        ...s,
        seasonCount: seasons.length,
        episodeCount: episodes.length,
        boundEpisodeCount: episodes.filter((e) => e.bindings.length > 0).length,
        bindingCount: bindings.length,
        deadBindingCount: bindings.filter((b) => b.status === 'dead').length,
      }
    }),
  )
  vi.mocked(getSeries).mockImplementation(async (id) => {
    const series = all.find((s) => s.id === id)
    if (!series) throw new ApiError('剧不存在', 1, 404)
    return structuredClone(series)
  })
})

afterEach(() => {
  vi.useRealTimers()
  vi.resetAllMocks()
})

const seasonNav = () => within(screen.getByRole('navigation', { name: '季' }))
/** 标题为 title 的绑定卡片 */
const card = async (title: string) => within(await screen.findByRole('article', { name: title }))

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
    expect(screen.getByText(/季 ID 11/)).toBeInTheDocument()
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
    expect(screen.getByText(/季 ID 11/)).toBeInTheDocument()
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

describe('绑定', () => {
  it('剧列表、集列表和季面板显示绑定统计', async () => {
    renderRoutes('/catalog/1')

    const list = within(await screen.findByRole('complementary'))
    // 已绑定集数/总集数，有失效绑定时显示失效数
    expect(await list.findByRole('link', { name: /星海旅人/ })).toHaveTextContent(/1\/3\s*1 失效$/)
    expect(list.getByRole('link', { name: /长夜灯塔/ })).toHaveTextContent(/0\/1$/)

    // 集列表：绑定数，有失效绑定时标红；没有绑定时不显示
    expect(
      within(screen.getByRole('link', { name: /启程/ })).getByTitle('2 个绑定，1 个失效'),
    ).toHaveTextContent('2')
    expect(
      within(screen.getByRole('link', { name: /^2/ })).queryByTitle(/个绑定/),
    ).not.toBeInTheDocument()

    expect(screen.getByText(/季 ID 11/)).toHaveTextContent(
      '2 集 · 已绑定 1 集 · 1 个失效绑定 · 季 ID 11',
    )
  })

  it('集面板：每个绑定一张只读卡片', async () => {
    renderRoutes('/catalog/1/11/110')

    expect(await screen.findByRole('heading', { name: '绑定（2）' })).toBeInTheDocument()
    const first = within(screen.getByRole('article', { name: '弹幕源 1' }))
    expect(first.getByText('正常')).toBeInTheDocument()
    expect(first.getByRole('link', { name: '弹幕源 1' })).toHaveAttribute(
      'href',
      'https://www.bilibili.com/video/BV1xx411c7X1',
    )
    expect(first.getByText('B 站投稿 BV1xx411c7X1')).toBeInTheDocument()
    expect(first.getByText('弹幕 1,234 条')).toBeInTheDocument()
    // 与本集时长相差 3 秒以上时标出
    expect(first.getByText(/^弹幕源 23:47/)).toHaveTextContent(
      '弹幕源 23:47 / 本集 23:40（弹幕源长 7 秒）',
    )

    const second = within(screen.getByRole('article', { name: '弹幕源 2' }))
    expect(second.getByText('失效')).toBeInTheDocument()
    expect(second.getByText(/^弹幕源 23:38/)).toHaveTextContent('弹幕源 23:38 / 本集 23:40')
  })

  it('没有绑定的集提示贴链接', async () => {
    renderRoutes('/catalog/1/11/111')

    expect(await screen.findByRole('heading', { name: '绑定（0）' })).toBeInTheDocument()
    expect(screen.getByText(/还没有绑定/)).toBeInTheDocument()
  })

  it('贴链接：进行中显示已用秒数，成功后用 toast 提示并显示新的绑定', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    let finish!: () => void
    vi.mocked(createBinding).mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = () => {
            const created = binding(9, { title: '新的弹幕源', danmakuCount: 2345 })
            all[0]!.seasons[1]!.episodes[1]!.bindings.push(created)
            resolve(created)
          }
        }),
    )
    renderRoutes('/catalog/1/11/111')

    const input = await screen.findByRole('textbox', { name: '弹幕源链接' })
    fireEvent.change(input, { target: { value: ' BV1xx411c7XX ' } })
    fireEvent.click(screen.getByRole('button', { name: '绑定' }))

    expect(await screen.findByText('正在解析链接并拉取全部弹幕，最长约 25 秒…')).toBeInTheDocument()
    expect(createBinding).toHaveBeenCalledWith(111, 'BV1xx411c7XX')
    expect(input).toBeDisabled()
    await act(() => vi.advanceTimersByTimeAsync(3000))
    expect(screen.getByRole('button', { name: /^拉取中/ })).toHaveTextContent('拉取中 3s')

    act(() => finish())

    expect(await screen.findByText('已绑定，拉取到 2,345 条弹幕')).toBeInTheDocument()
    expect(await screen.findByRole('article', { name: '新的弹幕源' })).toBeInTheDocument()
    expect(input).toHaveValue('')
    expect(screen.queryByText(/最长约 25 秒/)).not.toBeInTheDocument()
  })

  it('贴链接失败：提示显示在输入框下方，保留到下次提交或手动关闭', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.mocked(createBinding).mockRejectedValue(new ApiError('视频不存在、已删除或不可见', 1, 422))
    renderRoutes('/catalog/1/11/111')

    const input = await screen.findByRole('textbox', { name: '弹幕源链接' })
    fireEvent.change(input, { target: { value: 'BV1xx411c7XX' } })
    fireEvent.click(screen.getByRole('button', { name: '绑定' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('视频不存在、已删除或不可见')
    // 在输入框所在的表单里，排在输入框之后
    expect(alert.closest('form')).toBe(input.closest('form'))
    expect(input.compareDocumentPosition(alert) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // 不会自动消失，改链接也不清除；链接保留，方便改了再试
    expect(input).toHaveValue('BV1xx411c7XX')
    await act(() => vi.advanceTimersByTimeAsync(10_000))
    fireEvent.change(input, { target: { value: 'BV1xx411c7XY' } })
    expect(screen.getByRole('alert')).toBeInTheDocument()

    // 手动关闭
    fireEvent.click(within(alert).getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())

    // 再失败一次，下次提交时清除
    fireEvent.click(screen.getByRole('button', { name: '绑定' }))
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    vi.mocked(createBinding).mockReturnValue(new Promise(() => {}))
    fireEvent.click(screen.getByRole('button', { name: '绑定' }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(screen.getByRole('button', { name: /^拉取中/ })).toBeInTheDocument()
  })

  it('换一集时不带着上一集的失败提示', async () => {
    vi.mocked(createBinding).mockRejectedValue(new ApiError('无法识别的链接', 1, 400))
    renderRoutes('/catalog/1/11/111')

    fireEvent.change(await screen.findByRole('textbox', { name: '弹幕源链接' }), {
      target: { value: 'abc' },
    })
    fireEvent.click(screen.getByRole('button', { name: '绑定' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('无法识别的链接')

    fireEvent.click(screen.getByRole('link', { name: /启程/ }))

    expect(await screen.findByRole('heading', { name: '第 1 集启程' })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: '弹幕源链接' })).toHaveValue('')
  })
})

describe('维护绑定', () => {
  /** 服务端「启程」这一集的绑定，用例改它来模拟后端的修改 */
  const bindingsOf110 = () => all[0]!.seasons[1]!.episodes[0]!.bindings

  it('重新拉取：进行中显示已用秒数，成功后用 toast 显示新增条数', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    let finish!: () => void
    vi.mocked(refetchBinding).mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = () => {
            const b = bindingsOf110()[0]!
            b.danmakuCount += 12
            resolve({ binding: b, added: 12 })
          }
        }),
    )
    renderRoutes('/catalog/1/11/110')
    const first = await card('弹幕源 1')

    fireEvent.click(first.getByRole('button', { name: '重新拉取' }))

    await waitFor(() => expect(refetchBinding).toHaveBeenCalledWith(1, false))
    expect(await first.findByText('正在拉取全部弹幕，最长约 25 秒…')).toBeInTheDocument()
    await act(() => vi.advanceTimersByTimeAsync(3000))
    expect(first.getByRole('button', { name: /^拉取中/ })).toHaveTextContent('拉取中 3s')
    expect(first.getByRole('button', { name: '删除' })).toBeDisabled()

    act(() => finish())

    expect(await screen.findByText('新增 12 条弹幕')).toBeInTheDocument()
    expect(await first.findByText('弹幕 1,246 条')).toBeInTheDocument()
    expect(first.queryByText(/最长约 25 秒/)).not.toBeInTheDocument()
  })

  it('清空后重新拉取：确认框写明后果，确认后才拉取', async () => {
    vi.mocked(refetchBinding).mockResolvedValue({
      binding: binding(1, { danmakuCount: 1100 }),
      added: 1100,
    })
    renderRoutes('/catalog/1/11/110')
    const first = await card('弹幕源 1')

    fireEvent.click(first.getByRole('button', { name: '清空后重新拉取' }))

    const dialog = within(await screen.findByRole('alertdialog'))
    expect(dialog.getByText('先完整拉取一遍，成功后替换现有弹幕。')).toBeInTheDocument()
    expect(
      dialog.getByText('B 站上已经删除、或已经滑出滚动窗口的弹幕会永久丢失。'),
    ).toBeInTheDocument()
    expect(dialog.getByText('拉取失败时不做任何改动。')).toBeInTheDocument()
    expect(refetchBinding).not.toHaveBeenCalled()

    fireEvent.click(dialog.getByRole('button', { name: '清空并重新拉取' }))

    await waitFor(() => expect(refetchBinding).toHaveBeenCalledWith(1, true))
    expect(await screen.findByText('已清空并重新拉取，共 1,100 条弹幕')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
  })

  it('重新拉取失败：提示显示在这张卡片下方；弹幕源不存在时重新加载这部剧，显示失效', async () => {
    vi.mocked(refetchBinding).mockRejectedValue(new ApiError('B 站接口异常', 1, 502))
    renderRoutes('/catalog/1/11/110')
    const article = await screen.findByRole('article', { name: '弹幕源 1' })
    const first = within(article)

    fireEvent.click(first.getByRole('button', { name: '重新拉取' }))

    const alert = await first.findByRole('alert')
    expect(alert).toHaveTextContent('B 站接口异常')
    expect(article.lastElementChild).toBe(alert)
    expect(
      within(screen.getByRole('article', { name: '弹幕源 2' })).queryByRole('alert'),
    ).toBeNull()
    // 临时错误：状态不变，不重新加载
    expect(getSeries).toHaveBeenCalledTimes(1)

    // 弹幕源不存在：后端已把绑定标为失效
    vi.mocked(refetchBinding).mockImplementation(async () => {
      bindingsOf110()[0]!.status = 'dead'
      throw new ApiError('视频不存在、已删除或不可见', 1, 422)
    })
    fireEvent.click(first.getByRole('button', { name: '重新拉取' }))

    expect(await first.findByText('失效')).toBeInTheDocument()
    expect(first.getByRole('alert')).toHaveTextContent('视频不存在、已删除或不可见')

    // 保留到手动关闭
    fireEvent.click(within(first.getByRole('alert')).getByRole('button', { name: '关闭' }))
    await waitFor(() => expect(first.queryByRole('alert')).not.toBeInTheDocument())
  })

  it('偏移：回车或失焦时保存，不合法时在前端拦下', async () => {
    vi.mocked(updateBindingOffset).mockImplementation(async (id, offset) => {
      const b = bindingsOf110().find((x) => x.id === id)!
      b.offset = offset
      return b
    })
    renderRoutes('/catalog/1/11/110')
    const first = await card('弹幕源 1')
    const input = first.getByRole('textbox', { name: '偏移（秒）' })
    expect(input).toHaveValue('0')

    // 没改动：不保存
    fireEvent.blur(input)
    expect(updateBindingOffset).not.toHaveBeenCalled()

    // 不合法：拦下，提示在卡片下方，输入保留
    fireEvent.change(input, { target: { value: '90000' } })
    fireEvent.blur(input)
    expect(first.getByRole('alert')).toHaveTextContent(
      '偏移必须是 -86400 到 86400 之间的秒数，小数最多三位',
    )
    expect(updateBindingOffset).not.toHaveBeenCalled()
    expect(input).toHaveValue('90000')
    expect(input).toHaveAttribute('aria-invalid', 'true')

    // 回车保存，提示清除
    fireEvent.change(input, { target: { value: '-2.5' } })
    act(() => input.focus())
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(updateBindingOffset).toHaveBeenCalledWith(1, -2.5))
    expect(await screen.findByText('偏移已改为 -2.5 秒')).toBeInTheDocument()
    expect(first.queryByRole('alert')).not.toBeInTheDocument()
    await waitFor(() =>
      expect(first.getByRole('textbox', { name: '偏移（秒）' })).toHaveValue('-2.5'),
    )
  })

  it('删除绑定：确认框写明它的弹幕会一起删除，确认后才删除', async () => {
    vi.mocked(deleteBinding).mockImplementation(async (id) => {
      const episode = all[0]!.seasons[1]!.episodes[0]!
      episode.bindings = episode.bindings.filter((b) => b.id !== id)
      return null
    })
    renderRoutes('/catalog/1/11/110')
    const first = await card('弹幕源 1')

    // 取消：不删除
    fireEvent.click(first.getByRole('button', { name: '删除' }))
    fireEvent.click(
      within(await screen.findByRole('alertdialog')).getByRole('button', { name: '取消' }),
    )
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(deleteBinding).not.toHaveBeenCalled()

    fireEvent.click(first.getByRole('button', { name: '删除' }))
    const dialog = within(await screen.findByRole('alertdialog'))
    expect(dialog.getByText(/弹幕会一起删除/)).toHaveTextContent(
      '「弹幕源 1」的 1,234 条弹幕会一起删除，无法恢复。',
    )
    fireEvent.click(dialog.getByRole('button', { name: '删除' }))

    await waitFor(() => expect(deleteBinding).toHaveBeenCalledWith(1))
    expect(await screen.findByText('已删除绑定')).toBeInTheDocument()
    expect(await screen.findByRole('heading', { name: '绑定（1）' })).toBeInTheDocument()
    expect(screen.queryByRole('article', { name: '弹幕源 1' })).not.toBeInTheDocument()
  })
})
