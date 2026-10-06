import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'

import {
  advance,
  binding,
  card,
  lighthouse,
  mockCatalog,
  mockRootLayout,
  renderRoutes,
} from '@/__tests__/utils'
import { ApiError } from '@/api/request'
import {
  backfillSeasonBinding,
  createSeasonBinding,
  deleteSeasonBinding,
  getDefaultEpisodePatterns,
  getSeasonBinding,
  previewSeasonBinding,
  updateSeasonBinding,
  type CollectionCandidate,
  type SeasonBinding,
  type SeasonBindingItem,
} from '@/api/season-bindings'
import { getSeries, type SeriesDetail } from '@/api/series'

vi.mock('@/api/bindings')
vi.mock('@/api/season-bindings')
vi.mock('@/api/series')
// 根布局会取最近一次同步和设置
vi.mock('@/api/settings')
vi.mock('@/api/sync')

/** 默认的集号规则（假的，只要是两条） */
const defaultPatterns = ['第(\\d+)集', 'EP(\\d+)']

function seasonBinding(id: number, patch: Partial<SeasonBinding> = {}): SeasonBinding {
  return {
    id,
    seasonId: 11,
    adapter: 'bilibili',
    sourceUrl: 'https://www.bilibili.com/bangumi/play/ss41410',
    sourceLabel: 'B 站番剧 ss41410',
    title: '星海旅人 第一季',
    finished: false,
    mappingFrom: 1,
    mappingTo: 1,
    numberedByRule: false,
    episodePatterns: defaultPatterns,
    follow: true,
    status: 'active',
    lastError: null,
    lastCheckedAt: '2026-10-05T08:00:00Z',
    running: false,
    bindingCount: 1,
    ...patch,
  }
}

/** 星海旅人的第 1 季（季 11）：第 1 集（集 110）有一个季绑定 1 建出的绑定，第 2 集（集 111）没有绑定 */
function starVoyager(): SeriesDetail {
  return {
    id: 1,
    type: 'tv',
    title: '星海旅人',
    originalTitle: null,
    year: 2019,
    posterImageId: null,
    seasons: [
      {
        id: 11,
        number: 1,
        title: null,
        seasonBindings: [seasonBinding(1)],
        episodes: [
          {
            id: 110,
            number: 1,
            title: '启程',
            duration: 1420,
            bindings: [binding(1, { seasonBindingId: 1 })],
          },
          { id: 111, number: 2, title: null, duration: 1420, bindings: [] },
        ],
      },
    ],
  }
}

/** 服务端的目录与各季绑定的条目表，mock 的接口按它返回；用例改它来模拟后端的变化 */
let all: SeriesDetail[]
let items: Record<number, SeasonBindingItem[]>

/** 服务端的季绑定 */
function serverBinding(id: number) {
  return all
    .flatMap((s) => s.seasons)
    .flatMap((se) => se.seasonBindings)
    .find((sb) => sb.id === id)!
}

beforeEach(() => {
  all = [starVoyager(), lighthouse()]
  items = {}
  mockRootLayout()
  mockCatalog(() => all)
  vi.mocked(getDefaultEpisodePatterns).mockResolvedValue({ episodePatterns: defaultPatterns })
  vi.mocked(getSeasonBinding).mockImplementation(async (id) => ({
    ...structuredClone(serverBinding(id)),
    items: structuredClone(items[id] ?? []),
  }))
})

afterEach(() => {
  vi.useRealTimers()
  vi.resetAllMocks()
})

/** Re:0 第二季后半那样接着编号的番剧：B 站从第 14 集起，本季从第 1 集起 */
const continued: CollectionCandidate = {
  kind: 'bangumi',
  title: '星海旅人 第二部分',
  sourceUrl: 'https://www.bilibili.com/bangumi/play/ss36429',
  sourceLabel: 'B 站番剧 ss36429',
  finished: true,
  numberedByRule: false,
  items: [
    { label: '第13话 回顾', number: 13, reason: null },
    { label: '第14话 再出发', number: 14, reason: null },
    { label: '第15话 归途', number: 15, reason: null },
    { label: '第16话 终章', number: 16, reason: null },
    { label: 'SP 总集篇', number: null, reason: '集号「SP」不是整数' },
  ],
}

/** 预览表格各行"对到本地"一栏的文字 */
function targets() {
  const table = within(screen.getByRole('table', { name: '对应表' }))
  return table
    .getAllByRole('row')
    .slice(1)
    .map((row) => within(row).getAllByRole('cell')[2]!.textContent)
}

async function previewLink(link: string) {
  fireEvent.change(await screen.findByRole('textbox', { name: '合集的链接' }), {
    target: { value: link },
  })
  fireEvent.click(screen.getByRole('button', { name: '预览' }))
}

describe('添加季绑定', () => {
  it('预览预填 1 = 1 的集号对应，改对应时表格立即刷新；创建后卡片出现并显示补建进度，结束后刷新剧详情', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.mocked(previewSeasonBinding).mockResolvedValue({
      candidates: [continued],
    })
    vi.mocked(createSeasonBinding).mockImplementation(async () => {
      const sb = seasonBinding(2, {
        title: continued.title,
        sourceLabel: continued.sourceLabel,
        sourceUrl: continued.sourceUrl,
        finished: true,
        mappingFrom: 13,
        running: true,
        bindingCount: 0,
        lastCheckedAt: null,
      })
      all[0]!.seasons[0]!.seasonBindings.push(sb)
      return { ...structuredClone(sb), items: [] }
    })
    renderRoutes('/catalog/1/11')

    await previewLink(' ss36429 ')

    await waitFor(() =>
      expect(previewSeasonBinding).toHaveBeenCalledWith(11, 'ss36429', defaultPatterns),
    )
    // 番剧的集号由平台给出，没有集号规则
    expect(screen.queryByRole('group', { name: '集号规则' })).not.toBeInTheDocument()
    const preview = within(await screen.findByRole('region', { name: '预览' }))
    expect(preview.getByRole('link', { name: '星海旅人 第二部分' })).toHaveAttribute(
      'href',
      'https://www.bilibili.com/bangumi/play/ss36429',
    )
    expect(preview.getByText('B 站番剧 ss36429 · 共 5 条')).toBeInTheDocument()
    // 追更的说明按配置的时间规则写出
    expect(
      await preview.findByText(
        '已完结，可以关掉追更：新建的季绑定开着追更，建出的绑定在 336 小时内每 12 小时自动重新拉取一次。',
      ),
    ).toBeInTheDocument()
    expect(preview.getByRole('textbox', { name: '合集第几集' })).toHaveValue('1')
    expect(preview.getByRole('textbox', { name: '本地第几集' })).toHaveValue('1')
    // 接着编号的番剧：手动改成合集第 14 集 = 本地第 1 集
    fireEvent.change(preview.getByRole('textbox', { name: '合集第几集' }), {
      target: { value: '14' },
    })
    expect(targets()).toEqual([
      '在起点之前',
      '第 1 集（已有 1 个绑定）',
      '第 2 集',
      '目录里还没有第 3 集',
      '对不上：集号「SP」不是整数',
    ])

    fireEvent.change(preview.getByRole('textbox', { name: '合集第几集' }), {
      target: { value: '13' },
    })
    expect(targets()).toEqual([
      '第 1 集（已有 1 个绑定）',
      '第 2 集',
      '目录里还没有第 3 集',
      '目录里还没有第 4 集',
      '对不上：集号「SP」不是整数',
    ])
    // 不合法的对应不能创建
    fireEvent.change(preview.getByRole('textbox', { name: '本地第几集' }), {
      target: { value: '-1' },
    })
    expect(preview.getByRole('button', { name: '创建' })).toBeDisabled()
    fireEvent.change(preview.getByRole('textbox', { name: '本地第几集' }), {
      target: { value: '1' },
    })

    fireEvent.click(preview.getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(createSeasonBinding).toHaveBeenCalledWith(11, {
        link: 'ss36429',
        kind: 'bangumi',
        mappingFrom: 13,
        mappingTo: 1,
        episodePatterns: defaultPatterns,
      }),
    )
    expect(await screen.findByText('已创建季绑定，正在后台补建')).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: '预览' })).not.toBeInTheDocument(),
    )
    expect(screen.getByRole('textbox', { name: '合集的链接' })).toHaveValue('')
    const created = await card('星海旅人 第二部分')
    expect(created.getByText(/^补建中/)).toBeInTheDocument()
    expect(created.getByText('还没有检查')).toBeInTheDocument()

    // 补建进行中每秒轮询详情，建出的绑定数逐条更新
    const polls = vi.mocked(getSeasonBinding).mock.calls.length
    serverBinding(2).bindingCount = 1
    await advance(1000)
    expect(vi.mocked(getSeasonBinding).mock.calls.length).toBeGreaterThan(polls)
    expect(created.getByText(/^补建中/)).toBeInTheDocument()
    expect(await created.findByText('建出 1 个绑定')).toBeInTheDocument()

    // 补建结束：停止轮询，剧详情随之刷新
    Object.assign(serverBinding(2), {
      running: false,
      bindingCount: 2,
      lastCheckedAt: '2026-10-05T09:00:00Z',
    })
    const reloads = vi.mocked(getSeries).mock.calls.length
    await advance(1000)
    await waitFor(() => expect(created.queryByText(/^补建中/)).not.toBeInTheDocument())
    await waitFor(() => expect(vi.mocked(getSeries).mock.calls.length).toBeGreaterThan(reloads))
    expect(await created.findByText('建出 2 个绑定')).toBeInTheDocument()
    const stopped = vi.mocked(getSeasonBinding).mock.calls.length
    await advance(3000)
    expect(getSeasonBinding).toHaveBeenCalledTimes(stopped)
  })

  it('链接对应多个合集：先选一个再显示对应表；改集号规则后重新预览才能创建，选中的候选保留', async () => {
    const pages: CollectionCandidate = {
      kind: 'multiPage',
      title: '星海旅人 全集',
      sourceUrl: 'https://www.bilibili.com/video/BV17x411w7KC',
      sourceLabel: 'B 站多 P 投稿 BV17x411w7KC',
      finished: false,
      numberedByRule: true,
      items: [{ label: '星海旅人 全集 / 01', number: 1, reason: null }],
    }
    const collection: CollectionCandidate = {
      ...pages,
      kind: 'ugcSeason',
      title: '搬运合集',
      sourceLabel: 'B 站投稿合集 8597253',
      items: [
        { label: '星海旅人 全集 / 01', number: null, reason: '不符合集号规则' },
        { label: '星海旅人 第1集', number: 1, reason: null },
        { label: '星海旅人 第2集 PV', number: 2, reason: null },
      ],
    }
    vi.mocked(previewSeasonBinding).mockResolvedValue({
      candidates: [pages, collection],
    })
    vi.mocked(createSeasonBinding).mockReturnValue(new Promise(() => {}))
    renderRoutes('/catalog/1/11')

    await previewLink('BV17x411w7KC')

    const choices = within(await screen.findByRole('radiogroup', { name: '选择要绑定的合集' }))
    expect(choices.getAllByRole('radio').map((r) => r.textContent)).toEqual([
      '这个稿件《星海旅人 全集》的 1 个分 PB 站多 P 投稿 BV17x411w7KC',
      '它所在的合集《搬运合集》共 3 条B 站投稿合集 8597253',
    ])
    expect(screen.queryByRole('table', { name: '对应表' })).not.toBeInTheDocument()

    fireEvent.click(choices.getByRole('radio', { name: /搬运合集/ }))

    expect(choices.getByRole('radio', { name: /搬运合集/ })).toHaveAttribute('aria-checked', 'true')
    expect(targets()).toEqual(['对不上：不符合集号规则', '第 1 集（已有 1 个绑定）', '第 2 集'])

    // 改了规则：重新预览之前不能创建。编辑的起点是默认规则
    const preview = within(screen.getByRole('region', { name: '预览' }))
    expect(preview.getByRole('textbox', { name: '集号规则第 1 条' })).toHaveValue('第(\\d+)集')
    fireEvent.change(preview.getByRole('textbox', { name: '集号规则第 1 条' }), {
      target: { value: ' 第(\\d+)集$ ' },
    })
    fireEvent.click(preview.getByRole('button', { name: '删除第 2 条' }))
    expect(preview.getByRole('button', { name: '删除第 1 条' })).toBeDisabled()
    expect(preview.getByRole('button', { name: '创建' })).toBeDisabled()
    expect(preview.getByText('集号规则改了，先重新预览')).toBeInTheDocument()

    // 规则不合法：提示显示在规则下方，预览还在
    vi.mocked(previewSeasonBinding).mockRejectedValueOnce(
      new ApiError('集号规则不是合法的正则：missing closing )', 1, 400),
    )
    fireEvent.click(preview.getByRole('button', { name: '重新预览' }))
    expect(await preview.findByRole('alert')).toHaveTextContent('集号规则不是合法的正则')
    expect(screen.getByRole('table', { name: '对应表' })).toBeInTheDocument()

    vi.mocked(previewSeasonBinding).mockResolvedValue({
      candidates: [
        pages,
        {
          ...collection,
          items: [
            { label: '星海旅人 全集 / 01', number: null, reason: '不符合集号规则' },
            { label: '星海旅人 第1集', number: 1, reason: null },
            { label: '星海旅人 第2集 PV', number: null, reason: '不符合集号规则' },
          ],
        },
      ],
    })
    fireEvent.click(preview.getByRole('button', { name: '重新预览' }))

    await waitFor(() =>
      expect(previewSeasonBinding).toHaveBeenLastCalledWith(11, 'BV17x411w7KC', ['第(\\d+)集$']),
    )
    await waitFor(() =>
      expect(targets()).toEqual([
        '对不上：不符合集号规则',
        '第 1 集（已有 1 个绑定）',
        '对不上：不符合集号规则',
      ]),
    )
    const repreviewed = within(screen.getByRole('region', { name: '预览' }))
    expect(repreviewed.getByRole('radio', { name: /搬运合集/ })).toHaveAttribute(
      'aria-checked',
      'true',
    )
    expect(repreviewed.getByRole('textbox', { name: '集号规则第 1 条' })).toHaveValue('第(\\d+)集$')
    expect(repreviewed.queryByRole('textbox', { name: '集号规则第 2 条' })).not.toBeInTheDocument()
    expect(repreviewed.queryByRole('alert')).not.toBeInTheDocument()

    fireEvent.click(repreviewed.getByRole('button', { name: '创建' }))
    await waitFor(() =>
      expect(createSeasonBinding).toHaveBeenCalledWith(11, {
        link: 'BV17x411w7KC',
        kind: 'ugcSeason',
        mappingFrom: 1,
        mappingTo: 1,
        episodePatterns: ['第(\\d+)集$'],
      }),
    )
  })

  it('预览失败、创建失败：提示显示在出错的位置；取消就当没发生过', async () => {
    vi.mocked(previewSeasonBinding).mockRejectedValue(new ApiError('暂不支持系列', 1, 400))
    renderRoutes('/catalog/1/11')

    await previewLink('https://space.bilibili.com/1/lists/2?type=series')

    const form = screen.getByRole('form', { name: '添加季绑定' })
    expect(await within(form).findByRole('alert')).toHaveTextContent('暂不支持系列')

    vi.mocked(previewSeasonBinding).mockResolvedValue({
      candidates: [continued],
    })
    vi.mocked(createSeasonBinding).mockRejectedValue(
      new ApiError('这一季已经绑定过这个合集', 1, 409),
    )
    fireEvent.click(screen.getByRole('button', { name: '预览' }))
    const preview = within(await screen.findByRole('region', { name: '预览' }))
    expect(within(form).queryByRole('alert')).not.toBeInTheDocument()

    fireEvent.click(preview.getByRole('button', { name: '创建' }))
    expect(await preview.findByRole('alert')).toHaveTextContent('这一季已经绑定过这个合集')

    fireEvent.click(preview.getByRole('button', { name: '取消' }))
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: '预览' })).not.toBeInTheDocument(),
    )
    expect(all[0]!.seasons[0]!.seasonBindings).toHaveLength(1)
  })
})

describe('季绑定卡片', () => {
  it('显示合集、建出的绑定数、上次检查的时间与错误、追更开关；失效时标红', async () => {
    Object.assign(all[0]!.seasons[0]!.seasonBindings[0]!, {
      status: 'dead',
      lastError: '番剧不存在、已下架或不可见',
      finished: true,
    })
    renderRoutes('/catalog/1/11')

    expect(await screen.findByRole('heading', { name: '季绑定（1）' })).toBeInTheDocument()
    const sb = await card('星海旅人 第一季')
    expect(sb.getByText('失效')).toBeInTheDocument()
    expect(sb.getByRole('link', { name: '星海旅人 第一季' })).toHaveAttribute(
      'href',
      'https://www.bilibili.com/bangumi/play/ss41410',
    )
    expect(sb.getByText('B 站番剧 ss41410 · 已完结')).toBeInTheDocument()
    expect(sb.getByText('建出 1 个绑定')).toBeInTheDocument()
    expect(sb.getByText(/^上次检查 /)).toBeInTheDocument()
    expect(sb.getByText('上次检查：番剧不存在、已下架或不可见')).toBeInTheDocument()
    expect(sb.getByRole('switch', { name: '追更' })).toBeChecked()
    expect(sb.getByRole('textbox', { name: '合集第几集' })).toHaveValue('1')
    expect(sb.queryByRole('group', { name: '集号规则' })).not.toBeInTheDocument()
    // 打开季面板不请求详情
    expect(getSeasonBinding).not.toHaveBeenCalled()
  })

  it('展开条目表：逐条显示状态', async () => {
    items[1] = [
      {
        label: '第1话',
        number: 1,
        state: 'bound',
        reason: null,
        episodeNumber: 1,
        lastErrorAt: null,
      },
      {
        label: '第2话',
        number: 2,
        state: 'failed',
        reason: 'B 站接口异常',
        episodeNumber: 2,
        lastErrorAt: '2026-10-05T08:00:00Z',
      },
      {
        label: '第3话',
        number: 3,
        state: 'waitingEpisode',
        reason: null,
        episodeNumber: 3,
        lastErrorAt: null,
      },
      {
        label: 'SP',
        number: null,
        state: 'unmatched',
        reason: '集号「SP」不是整数',
        episodeNumber: null,
        lastErrorAt: null,
      },
    ]
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')

    fireEvent.click(sb.getByRole('button', { name: '条目表' }))

    const table = within(await sb.findByRole('table', { name: '条目表' }))
    expect(
      table
        .getAllByRole('row')
        .slice(1)
        .map((r) => r.textContent),
    ).toEqual([
      '1第1话已建绑定：第 1 集',
      '2第2话最近一次失败：B 站接口异常',
      '3第3话等待目录里出现第 3 集',
      '—SP对不上：集号「SP」不是整数',
    ])
  })

  it('立即补建：开始后显示进度；正在补建时用 toast 提示', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.mocked(backfillSeasonBinding).mockImplementation(async () => {
      serverBinding(1).running = true
      return null
    })
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')

    fireEvent.click(sb.getByRole('button', { name: '立即补建' }))

    expect(await screen.findByText('已开始补建')).toBeInTheDocument()
    expect(await sb.findByText(/^补建中/)).toBeInTheDocument()

    vi.mocked(backfillSeasonBinding).mockRejectedValue(new ApiError('正在补建', 1, 409))
    fireEvent.click(sb.getByRole('button', { name: '立即补建' }))
    expect(await screen.findByText('正在补建')).toBeInTheDocument()
    expect(sb.queryByRole('alert')).not.toBeInTheDocument()

    serverBinding(1).running = false
    await advance(1000)
    await waitFor(() => expect(sb.queryByText(/^补建中/)).not.toBeInTheDocument())
  })

  it('改集号对应：改动后才能保存，保存后开始显示补建进度', async () => {
    vi.mocked(updateSeasonBinding).mockImplementation(async (id, patch) => {
      Object.assign(serverBinding(id), patch, { running: true })
      return { ...structuredClone(serverBinding(id)), items: [] }
    })
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')
    expect(sb.queryByRole('button', { name: '保存' })).not.toBeInTheDocument()

    fireEvent.change(sb.getByRole('textbox', { name: '合集第几集' }), { target: { value: '1.5' } })
    expect(sb.getByRole('button', { name: '保存' })).toBeDisabled()
    fireEvent.change(sb.getByRole('textbox', { name: '合集第几集' }), { target: { value: '14' } })
    fireEvent.click(sb.getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(updateSeasonBinding).toHaveBeenCalledWith(1, { mappingFrom: 14, mappingTo: 1 }),
    )
    expect(await screen.findByText('集号对应已保存，正在后台补建')).toBeInTheDocument()
    expect(await sb.findByText(/^补建中/)).toBeInTheDocument()
    await waitFor(() => expect(sb.queryByRole('button', { name: '保存' })).not.toBeInTheDocument())
  })

  it('改集号规则：只有按规则编号的合集显示；可以调整顺序、新增、恢复默认，改动后才能保存', async () => {
    Object.assign(serverBinding(1), { numberedByRule: true })
    vi.mocked(updateSeasonBinding).mockImplementation(async (id, patch) => {
      Object.assign(serverBinding(id), patch, { running: true })
      return { ...structuredClone(serverBinding(id)), items: [] }
    })
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')
    const values = () =>
      within(sb.getByRole('group', { name: '集号规则' }))
        .getAllByRole('textbox')
        .map((el) => (el as HTMLInputElement).value)
    expect(values()).toEqual(defaultPatterns)
    expect(sb.getByRole('button', { name: '上移第 1 条' })).toBeDisabled()
    expect(sb.getByRole('button', { name: '下移第 2 条' })).toBeDisabled()
    expect(sb.queryByRole('button', { name: '保存' })).not.toBeInTheDocument()

    // 调换顺序算改动，恢复默认之后又没有改动
    fireEvent.click(sb.getByRole('button', { name: '下移第 1 条' }))
    expect(values()).toEqual(['EP(\\d+)', '第(\\d+)集'])
    expect(sb.getByRole('button', { name: '保存' })).toBeInTheDocument()
    await waitFor(() => expect(sb.getByRole('button', { name: '恢复默认' })).toBeEnabled())
    fireEvent.click(sb.getByRole('button', { name: '恢复默认' }))
    expect(values()).toEqual(defaultPatterns)
    expect(sb.queryByRole('button', { name: '保存' })).not.toBeInTheDocument()

    // 新增一条、移到第 2 条；空着的一条提交时丢掉，每条去掉前后的空白
    fireEvent.click(sb.getByRole('button', { name: '新增' }))
    fireEvent.change(sb.getByRole('textbox', { name: '集号规则第 3 条' }), {
      target: { value: '第(\\d+)话 ' },
    })
    fireEvent.click(sb.getByRole('button', { name: '上移第 3 条' }))
    fireEvent.click(sb.getByRole('button', { name: '新增' }))
    expect(values()).toEqual(['第(\\d+)集', '第(\\d+)话 ', 'EP(\\d+)', ''])
    fireEvent.click(sb.getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(updateSeasonBinding).toHaveBeenCalledWith(1, {
        episodePatterns: ['第(\\d+)集', '第(\\d+)话', 'EP(\\d+)'],
      }),
    )
    expect(await screen.findByText('集号规则已保存，正在后台补建')).toBeInTheDocument()
    await waitFor(() => expect(sb.queryByRole('button', { name: '保存' })).not.toBeInTheDocument())
    expect(values()).toEqual(['第(\\d+)集', '第(\\d+)话', 'EP(\\d+)'])
  })

  it('关掉追更', async () => {
    vi.mocked(updateSeasonBinding).mockImplementation(async (id, patch) => {
      Object.assign(serverBinding(id), patch)
      return { ...structuredClone(serverBinding(id)), items: [] }
    })
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')

    fireEvent.click(sb.getByRole('switch', { name: '追更' }))

    await waitFor(() => expect(updateSeasonBinding).toHaveBeenCalledWith(1, { follow: false }))
    expect(await screen.findByText('已关闭追更')).toBeInTheDocument()
    await waitFor(() => expect(sb.getByRole('switch', { name: '追更' })).not.toBeChecked())
  })

  it('删除：确认框默认保留建出的绑定，勾选后一起删除；每次打开都恢复成不勾选', async () => {
    vi.mocked(deleteSeasonBinding).mockResolvedValue(null)
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')

    fireEvent.click(sb.getByRole('button', { name: '删除' }))
    let dialog = within(await screen.findByRole('alertdialog'))
    expect(dialog.getByText('它建出的 1 个绑定默认保留，变成普通绑定。')).toBeInTheDocument()
    fireEvent.click(dialog.getByRole('checkbox'))
    expect(dialog.getByRole('checkbox')).toBeChecked()
    fireEvent.click(dialog.getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())

    fireEvent.click(sb.getByRole('button', { name: '删除' }))
    dialog = within(await screen.findByRole('alertdialog'))
    expect(dialog.getByRole('checkbox')).not.toBeChecked()
    fireEvent.click(dialog.getByRole('checkbox'))
    act(() => fireEvent.click(dialog.getByRole('button', { name: '删除' })))

    await waitFor(() => expect(deleteSeasonBinding).toHaveBeenCalledWith(1, true))
    expect(await screen.findByText('已删除季绑定')).toBeInTheDocument()
  })
})

it('集面板：季绑定建出的绑定带"季绑定"标签', async () => {
  all[0]!.seasons[0]!.episodes[0]!.bindings.push(binding(2))
  renderRoutes('/catalog/1/11/110')

  expect(
    await within(await screen.findByRole('article', { name: '弹幕源 1' })).findByText('季绑定'),
  ).toBeInTheDocument()
  expect(
    within(screen.getByRole('article', { name: '弹幕源 2' })).queryByText('季绑定'),
  ).not.toBeInTheDocument()
})

it('剧列表：追更中的剧带标记，可以只看追更中', async () => {
  renderRoutes('/catalog')
  const list = within(await screen.findByRole('complementary'))
  expect(await list.findByText('2 部')).toBeInTheDocument()
  expect(
    within(list.getByRole('link', { name: /星海旅人/ })).getByRole('img', { name: '追更中' }),
  ).toBeInTheDocument()
  expect(
    within(list.getByRole('link', { name: /长夜灯塔/ })).queryByRole('img', { name: '追更中' }),
  ).not.toBeInTheDocument()

  const toggle = list.getByRole('button', { name: '只看追更中' })
  fireEvent.click(toggle)

  expect(toggle).toHaveAttribute('aria-pressed', 'true')
  expect(list.getByText('1 部')).toBeInTheDocument()
  expect(list.queryByRole('link', { name: /长夜灯塔/ })).not.toBeInTheDocument()

  // 与关键词同时生效
  fireEvent.change(list.getByRole('textbox', { name: '筛选剧名或原名' }), {
    target: { value: '灯塔' },
  })
  expect(list.getByText('没有匹配的剧')).toBeInTheDocument()
  fireEvent.click(toggle)
  expect(list.getByRole('link', { name: /长夜灯塔/ })).toBeInTheDocument()
})
