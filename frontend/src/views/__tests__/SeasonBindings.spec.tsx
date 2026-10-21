import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'

import {
  advance,
  binding,
  card,
  defaultPatterns,
  defaultEpisodeRule,
  episodeRuleDefaults,
  folderSeasonBinding,
  lighthouse,
  mockCatalog,
  mockRootLayout,
  picked,
  renderRoutes,
  seasonBinding,
} from '@/__tests__/utils'
import { ApiError } from '@/api/request'
import {
  backfillSeasonBinding,
  createCollection,
  createFolder,
  deleteSeasonBinding,
  getDefaultEpisodePatterns,
  getSeasonBinding,
  previewCollectionEdit,
  previewFolderEdit,
  previewNewCollection,
  previewNewFolder,
  saveCollection,
  saveFolder,
  setFollow,
  type Candidate,
  type Item,
  type SeasonBinding,
  type SlotView,
} from '@/api/season-bindings'
import { getSeries, type SeriesDetail } from '@/api/series'

vi.mock('@/api/bindings')
vi.mock('@/api/season-bindings')
vi.mock('@/api/series')
// 根布局会取最近一次同步和设置
vi.mock('@/api/settings')
vi.mock('@/api/sync')

/**
 * 星海旅人的第 1 季（季 11）：合集的季绑定 1；第 1 集（集 110）有一个季绑定 1 建出的绑定，
 * 第 2 集（集 111）没有绑定
 */
function starVoyager(): SeriesDetail {
  return {
    id: 1,
    type: 'tv',
    title: '星海旅人',
    originalTitle: null,
    year: 2019,
    premiereDate: null,
    tmdbId: null,
    posterImageId: null,
    sourceLink: null,
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

/** 服务端的目录与各季绑定的条目、没有条目的槽位，mock 的接口按它返回；用例改它来模拟后端的变化 */
let all: SeriesDetail[]
let parts: Record<number, { items: Item[]; emptySlots: SlotView[] }>

const season = () => all[0]!.seasons[0]!

/** 服务端的季绑定 */
function serverBinding(id: number) {
  return season().seasonBindings.find((b) => b.id === id)!
}

/** 服务端的季绑定的详情 */
function serverDetail(id: number) {
  return {
    ...structuredClone(serverBinding(id)),
    ...structuredClone(parts[id] ?? { items: [], emptySlots: [] }),
  }
}

/** 往季 11 加一个季绑定（服务端） */
function addBinding<T extends SeasonBinding>(sb: T) {
  season().seasonBindings.push(sb)
  return sb
}

beforeEach(() => {
  all = [starVoyager(), lighthouse()]
  parts = {}
  mockRootLayout()
  mockCatalog(() => all)
  vi.mocked(getDefaultEpisodePatterns).mockResolvedValue(episodeRuleDefaults)
  // mock 的类型按两种详情的联合，这里按服务端的季绑定拼出，种类总是对得上
  vi.mocked(getSeasonBinding).mockImplementation(async (id) => serverDetail(id) as never)
})

/** 一个条目派生的结果，默认是对到 episodeNumber 集、新建的 */
function item(label: string, number: number | null, patch: Partial<Item> = {}): Item {
  return {
    label,
    number,
    episodeNumber: number,
    status: 'create',
    reason: null,
    replaces: null,
    ...patch,
  }
}

/** 预览的一组，默认是接着编号的番剧：B 站从第 13 集起 */
function candidate(patch: Partial<Candidate> = {}): Candidate {
  return {
    collectionKind: 'bangumi',
    title: '星海旅人 第二部分',
    sourceUrl: 'https://www.bilibili.com/bangumi/play/ss36429',
    sourceLabel: 'B 站番剧 ss36429',
    finished: true,
    numberedByRule: false,
    current: false,
    items: [],
    removed: [],
    summary: { create: 0, replace: 0, remove: 0, keep: 0 },
    ...patch,
  }
}

/** 区域 region 里预览表各行的文字（序号、条目、保存后） */
function previewRows(region: ReturnType<typeof within>) {
  return within(region.getByRole('table', { name: '预览表' }))
    .getAllByRole('row')
    .slice(1)
    .map((row) =>
      within(row)
        .getAllByRole('cell')
        .map((c) => c.textContent),
    )
}

/** 区域 region 里预览的汇总标签 */
function tags(region: ReturnType<typeof within>) {
  return within(region.getByRole('region', { name: '预览' }))
    .getAllByText(/^(新建|替换|删除|保留) \d+$/)
    .map((t) => t.textContent)
}

/** 一个控制完成时机的 Promise */
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}

/** 在季面板上展开"绑定合集"，贴链接、点预览 */
async function openCollectionForm(link: string) {
  renderRoutes('/catalog/1/11')
  fireEvent.click(await screen.findByRole('button', { name: '绑定合集' }))
  const form = within(await screen.findByRole('region', { name: '绑定合集' }))
  fireEvent.change(await form.findByRole('textbox', { name: '合集的链接' }), {
    target: { value: link },
  })
  fireEvent.click(form.getByRole('button', { name: '预览' }))
  return form
}

/** 季面板右上角"导出 XML"的提示 */
const seasonExportTitle =
  '导出这一季各集的弹幕（zip，每集一个子目录、每个绑定一份 XML，按偏移校正）'

describe('季面板', () => {
  it('右上角两个添加按钮：表单在卡片列表上方展开，同一时间只展开一个；取消后收起', async () => {
    renderRoutes('/catalog/1/11')
    expect(await screen.findByRole('heading', { name: '季绑定（1）' })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '上传弹幕文件夹' }))

    const form = await screen.findByRole('region', { name: '上传弹幕文件夹' })
    expect(screen.getByRole('button', { name: '绑定合集' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '上传弹幕文件夹' })).toBeDisabled()
    // 在卡片列表上方
    expect(
      form.compareDocumentPosition(await screen.findByRole('article', { name: '星海旅人 第一季' })),
    ).toBe(Node.DOCUMENT_POSITION_FOLLOWING)

    fireEvent.click(within(form).getByRole('button', { name: '取消' }))
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: '上传弹幕文件夹' })).not.toBeInTheDocument(),
    )
    expect(screen.getByRole('button', { name: '绑定合集' })).toBeEnabled()
  })

  it('右上角"导出 XML"：这一季有弹幕时链接到导出接口由浏览器直接下载，整季都没有时禁用', async () => {
    all[0]!.seasons.push(
      {
        id: 12,
        number: 2,
        title: null,
        seasonBindings: [],
        episodes: [
          {
            id: 120,
            number: 1,
            title: null,
            duration: 1420,
            bindings: [binding(2, { danmakuCount: 0 })],
          },
          { id: 121, number: 2, title: null, duration: 1420, bindings: [] },
        ],
      },
      { id: 13, number: 3, title: null, seasonBindings: [], episodes: [] },
    )
    const { router } = renderRoutes('/catalog/1/11')

    // 第 1 集的绑定有弹幕，第 2 集没有绑定
    const link = await screen.findByTitle(seasonExportTitle)
    expect(link).toHaveRole('link')
    expect(link).toHaveTextContent('导出 XML')
    expect(link).toHaveAttribute('href', '/api/seasons/11/export')
    expect(link).toHaveAttribute('download')

    // 绑定都没有弹幕
    await act(() => router.navigate('/catalog/1/12'))
    expect(await screen.findByRole('heading', { name: '第 2 季' })).toBeInTheDocument()
    expect(screen.getByTitle(seasonExportTitle)).toBeDisabled()

    // 没有集
    await act(() => router.navigate('/catalog/1/13'))
    expect(await screen.findByRole('heading', { name: '第 3 季' })).toBeInTheDocument()
    expect(screen.getByTitle(seasonExportTitle)).toBeDisabled()
  })

  it('电影没有季面板，也就没有季的"导出 XML"，只有集面板的', async () => {
    all[1]!.seasons[0]!.episodes[0]!.bindings = [binding(5)]
    renderRoutes('/catalog/2')

    expect(
      await screen.findByRole('link', { name: '导出 XML', description: /这一集/ }),
    ).toBeInTheDocument()
    expect(screen.queryByTitle(seasonExportTitle)).not.toBeInTheDocument()
  })
})

describe('绑定合集', () => {
  it('贴链接预览：汇总和逐条目的表；改集号对应约 0.6 秒后自动重新预览，新的预览出来之前不能创建；创建后卡片显示补建进度', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    // 按集号对应派生：默认 1 = 1 时都在目录里没有，改成 13 = 1 后对到第 1、2 集
    vi.mocked(previewNewCollection).mockImplementation(async (_, form) => {
      const shifted = form.mappingFrom === 13
      return {
        candidates: [
          candidate({
            items: [
              item(
                '第13话 回顾',
                13,
                shifted ? { episodeNumber: 1, status: 'alreadyBound' } : { status: 'noEpisode' },
              ),
              item('第14话 再出发', 14, shifted ? { episodeNumber: 2 } : { status: 'noEpisode' }),
              item('SP 总集篇', null, { status: 'unmatched', reason: '平台没有给出整数集号' }),
            ],
            summary: { create: shifted ? 1 : 0, replace: 0, remove: 0, keep: 0 },
          }),
        ],
      }
    })
    vi.mocked(createCollection).mockImplementation(async () => {
      const sb = addBinding(
        seasonBinding(2, {
          title: '星海旅人 第二部分',
          mappingFrom: 13,
          running: true,
          bindingCount: 0,
          lastCheckedAt: null,
        }),
      )
      return { ...structuredClone(sb), items: [], emptySlots: [] }
    })

    const form = await openCollectionForm(' ss36429 ')

    expect(form.getByText('正在列出合集，最长约 25 秒…')).toBeInTheDocument()
    await waitFor(() =>
      expect(previewNewCollection).toHaveBeenCalledWith(11, {
        link: 'ss36429',
        episodeRule: defaultEpisodeRule,
        mappingFrom: 1,
        mappingTo: 1,
      }),
    )
    await form.findByRole('region', { name: '预览' })
    expect(form.getByRole('link', { name: '星海旅人 第二部分' })).toHaveAttribute(
      'href',
      'https://www.bilibili.com/bangumi/play/ss36429',
    )
    expect(form.getByText('B 站番剧 ss36429 · 共 3 条')).toBeInTheDocument()
    expect(
      await form.findByText('已完结，可以关掉追更：新建的季绑定开着追更，每 12 小时检查一次合集。'),
    ).toBeInTheDocument()
    // 番剧的集号由平台给出，没有集号规则
    expect(form.queryByRole('group', { name: '集号规则' })).not.toBeInTheDocument()
    expect(tags(form)).toEqual(['新建 0', '替换 0', '删除 0', '保留 0'])
    expect(previewRows(form)).toEqual([
      ['13', '第13话 回顾', '第 13 集 · 目录里没有这一集'],
      ['14', '第14话 再出发', '第 14 集 · 目录里没有这一集'],
      ['—', 'SP 总集篇', '对不上（平台没有给出整数集号）'],
    ])
    expect(form.getByRole('button', { name: '创建' })).toBeEnabled()

    // 改集号对应：新的预览出来之前不能创建
    fireEvent.change(form.getByRole('textbox', { name: '合集第几集' }), {
      target: { value: '13' },
    })
    expect(form.getByRole('button', { name: '创建' })).toBeDisabled()
    expect(previewNewCollection).toHaveBeenCalledTimes(1)
    await advance(600)
    await waitFor(() =>
      expect(previewNewCollection).toHaveBeenLastCalledWith(11, {
        link: 'ss36429',
        episodeRule: defaultEpisodeRule,
        mappingFrom: 13,
        mappingTo: 1,
      }),
    )
    await waitFor(() =>
      expect(previewRows(form)).toEqual([
        ['13', '第13话 回顾', '第 1 集 · 跳过：已有同源绑定'],
        ['14', '第14话 再出发', '第 2 集 · 新建'],
        ['—', 'SP 总集篇', '对不上（平台没有给出整数集号）'],
      ]),
    )
    expect(tags(form)).toEqual(['新建 1', '替换 0', '删除 0', '保留 0'])

    // 集号对应不合法：不预览，不能创建
    fireEvent.change(form.getByRole('textbox', { name: '本地第几集' }), {
      target: { value: '-1' },
    })
    await advance(600)
    expect(form.getByRole('button', { name: '创建' })).toBeDisabled()
    expect(previewNewCollection).toHaveBeenCalledTimes(2)
    fireEvent.change(form.getByRole('textbox', { name: '本地第几集' }), {
      target: { value: '1' },
    })
    await waitFor(() => expect(form.getByRole('button', { name: '创建' })).toBeEnabled())

    // 没有删除和替换：直接创建，不弹确认框
    fireEvent.click(form.getByRole('button', { name: '创建' }))

    await waitFor(() =>
      expect(createCollection).toHaveBeenCalledWith(11, {
        link: 'ss36429',
        collectionKind: 'bangumi',
        episodeRule: defaultEpisodeRule,
        mappingFrom: 13,
        mappingTo: 1,
      }),
    )
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(await screen.findByText('已创建季绑定，正在后台补建')).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: '绑定合集' })).not.toBeInTheDocument(),
    )
    const created = await card('星海旅人 第二部分')
    expect(created.getByText(/^补建中/)).toBeInTheDocument()
    expect(created.getByText('还没有检查')).toBeInTheDocument()

    // 补建进行中每秒轮询详情；结束后停止轮询，剧详情随之刷新
    Object.assign(serverBinding(2), { bindingCount: 1 })
    await advance(1000)
    expect(await created.findByText('建出 1 个绑定')).toBeInTheDocument()
    Object.assign(serverBinding(2), { running: false, lastCheckedAt: '2026-10-05T09:00:00Z' })
    const reloads = vi.mocked(getSeries).mock.calls.length
    await advance(1000)
    await waitFor(() => expect(created.queryByText(/^补建中/)).not.toBeInTheDocument())
    await waitFor(() => expect(vi.mocked(getSeries).mock.calls.length).toBeGreaterThan(reloads))
    const stopped = vi.mocked(getSeasonBinding).mock.calls.length
    await advance(3000)
    expect(getSeasonBinding).toHaveBeenCalledTimes(stopped)
  })

  it('链接对应多个合集：先选一个；按规则编号的合集才有集号规则，改了自动重新预览；创建时带上选中的候选', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const pages = candidate({
      collectionKind: 'multiPage',
      title: '星海旅人 全集',
      sourceUrl: 'https://www.bilibili.com/video/BV17x411w7KC',
      sourceLabel: 'B 站多 P 投稿 BV17x411w7KC',
      finished: false,
      numberedByRule: true,
      items: [item('01', 1)],
    })
    vi.mocked(previewNewCollection).mockImplementation(async (_, form) => ({
      candidates: [
        pages,
        candidate({
          ...pages,
          collectionKind: 'ugcSeason',
          title: '搬运合集',
          sourceLabel: 'B 站投稿合集 8597253',
          items: [
            item('星海旅人 第1集', 1, { status: 'keep' }),
            (form.episodeRule?.rules.length ?? form.episodePatterns?.length) === 1
              ? item('星海旅人 第2集 PV', null, { status: 'unmatched', reason: '认不出集号' })
              : item('星海旅人 第2集 PV', 2),
          ],
        }),
      ],
    }))
    vi.mocked(createCollection).mockReturnValue(new Promise(() => {}))

    const form = await openCollectionForm('BV17x411w7KC')

    const choices = within(await form.findByRole('radiogroup', { name: '选择要绑定的合集' }))
    expect(choices.getAllByRole('radio').map((r) => r.textContent)).toEqual([
      '这个稿件《星海旅人 全集》的 1 个分 PB 站多 P 投稿 BV17x411w7KC',
      '它所在的合集《搬运合集》共 2 条B 站投稿合集 8597253',
    ])
    // 还没选：没有预览表，不能创建
    expect(form.queryByRole('table', { name: '预览表' })).not.toBeInTheDocument()
    expect(form.getByRole('button', { name: '创建' })).toBeDisabled()

    fireEvent.click(choices.getByRole('radio', { name: /搬运合集/ }))

    expect(choices.getByRole('radio', { name: /搬运合集/ })).toHaveAttribute('aria-checked', 'true')
    expect(previewRows(form)).toEqual([
      ['1', '星海旅人 第1集', '第 1 集 · 保留'],
      ['2', '星海旅人 第2集 PV', '第 2 集 · 新建'],
    ])
    // 改集号规则：编辑的起点是默认规则，改完自动重新预览
    fireEvent.click(form.getByText('高级正则与排除规则'))
    expect(form.getByRole('textbox', { name: '集号规则第 1 条' })).toHaveValue('第(\\d+)集')
    fireEvent.click(form.getByRole('button', { name: '删除第 2 条' }))
    fireEvent.change(form.getByRole('textbox', { name: '集号规则第 1 条' }), {
      target: { value: ' 第(\\d+)集$ ' },
    })
    expect(form.getByRole('button', { name: '创建' })).toBeDisabled()
    await advance(600)
    await waitFor(() =>
      expect(previewNewCollection).toHaveBeenLastCalledWith(11, {
        link: 'BV17x411w7KC',
        episodeRule: { ...defaultEpisodeRule, rules: [{ pattern: '第(\\d+)集$', adjustment: 0 }] },
        mappingFrom: 1,
        mappingTo: 1,
      }),
    )
    await waitFor(() =>
      expect(previewRows(form)[1]).toEqual(['—', '星海旅人 第2集 PV', '对不上（认不出集号）']),
    )
    // 重新预览之后选中的候选不变
    expect(choices.getByRole('radio', { name: /搬运合集/ })).toHaveAttribute('aria-checked', 'true')

    fireEvent.click(form.getByRole('button', { name: '创建' }))
    await waitFor(() =>
      expect(createCollection).toHaveBeenCalledWith(11, {
        link: 'BV17x411w7KC',
        collectionKind: 'ugcSeason',
        episodeRule: { ...defaultEpisodeRule, rules: [{ pattern: '第(\\d+)集$', adjustment: 0 }] },
        mappingFrom: 1,
        mappingTo: 1,
      }),
    )
  })

  it('预览失败时不能创建，提示留在表单里；创建失败的提示也留在表单里', async () => {
    vi.mocked(previewNewCollection).mockRejectedValueOnce(new ApiError('暂不支持系列', 1, 400))
    const form = await openCollectionForm('https://space.bilibili.com/1/lists/2?type=series')

    expect(await form.findByRole('alert')).toHaveTextContent('暂不支持系列')
    expect(form.getByRole('button', { name: '创建' })).toBeDisabled()

    vi.mocked(previewNewCollection).mockResolvedValue({
      candidates: [candidate({ items: [item('第13话 回顾', 13, { status: 'noEpisode' })] })],
    })
    vi.mocked(createCollection).mockRejectedValue(new ApiError('这一季已经绑定过这个合集', 1, 409))
    fireEvent.change(form.getByRole('textbox', { name: '合集的链接' }), {
      target: { value: 'ss36429' },
    })
    fireEvent.click(form.getByRole('button', { name: '预览' }))
    await form.findByRole('region', { name: '预览' })
    await waitFor(() => expect(form.getByRole('button', { name: '创建' })).toBeEnabled())
    expect(form.queryByRole('alert')).not.toBeInTheDocument()

    fireEvent.click(form.getByRole('button', { name: '创建' }))
    expect(await form.findByRole('alert')).toHaveTextContent('这一季已经绑定过这个合集')
    expect(season().seasonBindings).toHaveLength(1)
  })
})

describe('编辑合集的季绑定', () => {
  it('旧规则升级仅修改草稿：预览过期时禁用保存，取消后重新打开仍为旧版', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    Object.assign(serverBinding(1), { numberedByRule: true, mappingTo: 13 })
    vi.mocked(previewCollectionEdit).mockResolvedValue({
      candidates: [candidate({ numberedByRule: true, current: true })],
    })
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')
    fireEvent.click(sb.getByRole('button', { name: '编辑' }))
    const form = within(sb.getByRole('region', { name: '编辑季绑定' }))
    await form.findByRole('region', { name: '预览' })
    const upgrade = form.getByRole('button', { name: '使用新版模板' })
    await waitFor(() => expect(upgrade).toBeEnabled())
    fireEvent.click(upgrade)
    expect(form.getByRole('button', { name: '保存' })).toBeDisabled()
    expect(form.getByText(/预览已过期/)).toBeInTheDocument()
    await advance(600)
    await waitFor(() =>
      expect(previewCollectionEdit).toHaveBeenLastCalledWith(1, {
        link: 'https://b23.tv/ss41410',
        episodeRule: defaultEpisodeRule,
        mappingFrom: 1,
        mappingTo: 13,
      }),
    )
    fireEvent.click(form.getByRole('button', { name: '取消' }))
    fireEvent.click(sb.getByRole('button', { name: '编辑' }))
    const reopened = within(sb.getByRole('region', { name: '编辑季绑定' }))
    expect(await reopened.findByText(/旧版规则/)).toBeInTheDocument()
    expect(saveCollection).not.toHaveBeenCalled()
  })

  it('打开就按保存的表单预览、选中保存的候选；没改也能保存，没有删除和替换时直接保存', async () => {
    vi.mocked(previewCollectionEdit).mockResolvedValue({
      candidates: [
        candidate({
          title: '星海旅人 第一季',
          current: true,
          items: [item('第1话', 1, { status: 'keep' }), item('第2话', 2)],
          summary: { create: 1, replace: 0, remove: 0, keep: 1 },
        }),
      ],
    })
    vi.mocked(saveCollection).mockImplementation(async (id) => {
      Object.assign(serverBinding(id), { running: true })
      return serverDetail(id) as never
    })
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')

    fireEvent.click(sb.getByRole('button', { name: '编辑' }))

    const form = within(sb.getByRole('region', { name: '编辑季绑定' }))
    expect(form.getByRole('textbox', { name: '合集的链接' })).toHaveValue('https://b23.tv/ss41410')
    await waitFor(() =>
      expect(previewCollectionEdit).toHaveBeenCalledWith(1, {
        link: 'https://b23.tv/ss41410',
        episodePatterns: defaultPatterns,
        mappingFrom: 1,
        mappingTo: 1,
      }),
    )
    await form.findByRole('region', { name: '预览' })
    expect(tags(form)).toEqual(['新建 1', '替换 0', '删除 0', '保留 1'])
    expect(form.getByRole('textbox', { name: '合集第几集' })).toHaveValue('1')
    await waitFor(() => expect(form.getByRole('button', { name: '保存' })).toBeEnabled())

    fireEvent.click(form.getByRole('button', { name: '保存' }))

    await waitFor(() =>
      expect(saveCollection).toHaveBeenCalledWith(1, {
        link: 'https://b23.tv/ss41410',
        collectionKind: 'bangumi',
        episodePatterns: defaultPatterns,
        mappingFrom: 1,
        mappingTo: 1,
      }),
    )
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(await screen.findByText('已保存，正在后台补建')).toBeInTheDocument()
    await waitFor(() =>
      expect(sb.queryByRole('region', { name: '编辑季绑定' })).not.toBeInTheDocument(),
    )
    expect(await sb.findByText(/^补建中/)).toBeInTheDocument()
  })

  it('有删除或替换时先确认，逐条列出要删的绑定、说明偏移归零；正在补建时的 409 留在表单里', async () => {
    const old = { episodeNumber: 1, bindingId: 1, title: '弹幕源 1', dead: false }
    vi.mocked(previewCollectionEdit).mockResolvedValue({
      candidates: [
        candidate({
          current: true,
          items: [item('第1话 重新投稿', 1, { status: 'replace', replaces: old })],
          removed: [{ episodeNumber: 2, bindingId: 7, title: '旧的 PV', dead: true }],
          summary: { create: 0, replace: 1, remove: 1, keep: 0 },
        }),
      ],
    })
    vi.mocked(saveCollection).mockRejectedValue(new ApiError('正在补建或保存，稍后再试', 1, 409))
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')
    fireEvent.click(sb.getByRole('button', { name: '编辑' }))
    const form = within(sb.getByRole('region', { name: '编辑季绑定' }))
    await form.findByRole('region', { name: '预览' })

    expect(form.getByText('删除 1')).toHaveAttribute('data-variant', 'destructive')
    expect(previewRows(form)).toEqual([
      ['1', '第1话 重新投稿', '第 1 集 · 替换（原：弹幕源 1）'],
      ['—', '旧的 PV', '第 2 集 · 删除（没有条目对到这一集）'],
    ])
    await waitFor(() => expect(form.getByRole('button', { name: '保存' })).toBeEnabled())

    fireEvent.click(form.getByRole('button', { name: '保存' }))

    const dialog = within(await screen.findByRole('alertdialog'))
    expect(dialog.getByText('第 2 集 · 旧的 PV')).toBeInTheDocument()
    expect(dialog.getByText(/偏移归零/)).toBeInTheDocument()
    expect(dialog.getByText('第 1 集 · 弹幕源 1 → 第1话 重新投稿')).toBeInTheDocument()
    expect(saveCollection).not.toHaveBeenCalled()
    act(() => fireEvent.click(dialog.getByRole('button', { name: '保存' })))

    await waitFor(() => expect(saveCollection).toHaveBeenCalledOnce())
    expect(await form.findByRole('alert')).toHaveTextContent('正在补建或保存，稍后再试')

    // 取消：收起编辑，什么都不改
    fireEvent.click(form.getByRole('button', { name: '取消' }))
    await waitFor(() =>
      expect(sb.queryByRole('region', { name: '编辑季绑定' })).not.toBeInTheDocument(),
    )
    expect(sb.getByRole('button', { name: '编辑' })).toBeEnabled()
  })
})

describe('季绑定卡片', () => {
  it('合集：状态、合集、来源、集号对应、建出的绑定数、上次检查与错误、追更开关；不写完结，不请求详情', async () => {
    Object.assign(serverBinding(1), { status: 'dead', lastError: '番剧不存在、已下架或不可见' })
    renderRoutes('/catalog/1/11')

    const sb = await card('星海旅人 第一季')
    expect(sb.getByText('失效')).toBeInTheDocument()
    expect(sb.getByRole('link', { name: '星海旅人 第一季' })).toHaveAttribute(
      'href',
      'https://www.bilibili.com/bangumi/play/ss41410',
    )
    expect(sb.getByText('B 站番剧 ss41410')).toBeInTheDocument()
    expect(sb.getByText('合集第 1 集 = 本地第 1 集')).toBeInTheDocument()
    expect(sb.getByText('建出 1 个绑定')).toBeInTheDocument()
    expect(sb.getByText(/^上次检查 /)).toBeInTheDocument()
    expect(sb.getByText('上次检查：番剧不存在、已下架或不可见')).toBeInTheDocument()
    expect(sb.getByRole('switch', { name: '追更' })).toBeChecked()
    expect(sb.queryByText(/已完结/)).not.toBeInTheDocument()
    expect(getSeasonBinding).not.toHaveBeenCalled()
  })

  it('文件夹：条目数、集号对应、上传时间，同样能编辑、立即补建、删除、展开条目表；没有追更和上次检查', async () => {
    addBinding(folderSeasonBinding(2, { entryCount: 12, bindingCount: 10, mappingFrom: 2 }))
    renderRoutes('/catalog/1/11')

    const sb = await card('来自新世界')
    expect(sb.getByText('上传的弹幕文件夹 · 12 个条目')).toBeInTheDocument()
    expect(sb.getByText('第 2 集 = 本地第 1 集')).toBeInTheDocument()
    expect(sb.getByText('建出 10 个绑定')).toBeInTheDocument()
    expect(sb.getByText(/^上传于 /)).toBeInTheDocument()
    for (const name of ['编辑', '立即补建', '删除', '条目表']) {
      expect(sb.getByRole('button', { name })).toBeInTheDocument()
    }
    expect(sb.queryByRole('switch')).not.toBeInTheDocument()
    expect(sb.queryByText(/检查/)).not.toBeInTheDocument()
    // 文件夹只在展开条目表时取详情
    expect(getSeasonBinding).not.toHaveBeenCalled()
  })

  it('展开条目表：逐条显示状态，上次失败的标红写出原因；没有条目、绑定还留着的槽位用黄色提示列出', async () => {
    parts[1] = {
      items: [
        item('第1话', 1, { status: 'keep' }),
        item('第2话', 2, {
          lastError: 'B 站限流，下次补建再试',
          lastErrorAt: '2026-10-05T08:00:00Z',
        }),
        item('第3话', 3, {
          status: 'replace',
          replaces: { episodeNumber: 3, bindingId: 5, title: '旧稿件', dead: true },
        }),
        item('SP', null, { status: 'unmatched', reason: '平台没有给出整数集号' }),
      ],
      emptySlots: [{ episodeNumber: 5, bindingId: 9, title: '第5话 已下架', dead: true }],
    }
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')

    fireEvent.click(sb.getByRole('button', { name: '条目表' }))

    const table = within(await sb.findByRole('table', { name: '条目表' }))
    const rows = table.getAllByRole('row').slice(1)
    expect(
      rows.map((r) =>
        within(r)
          .getAllByRole('cell')
          .map((c) => c.textContent),
      ),
    ).toEqual([
      ['1', '第1话', '第 1 集 · 已建绑定'],
      ['2', '第2话', '第 2 集 · 待补建 · 上次失败：B 站限流，下次补建再试'],
      ['3', '第3话', '第 3 集 · 待换新稿件（原：旧稿件）'],
      ['—', 'SP', '对不上（平台没有给出整数集号）'],
    ])
    expect(within(rows[1]!).getAllByRole('cell')[2]).toHaveClass('text-destructive')
    const note = within(sb.getByRole('note', { name: '没有条目的槽位' }))
    expect(note.getByText('没有条目、绑定还留着（1）')).toBeInTheDocument()
    expect(note.getByText('第 5 集 · 第5话 已下架（失效）')).toBeInTheDocument()
    expect(note.getByText(/补建不会删它们；编辑并保存一次季绑定会删掉/)).toBeInTheDocument()
  })

  it('立即补建：合集开始后显示补建中并轮询；正在补建时用 toast 提示', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.mocked(backfillSeasonBinding).mockImplementation(async (id) => {
      Object.assign(serverBinding(id), { running: true })
      return serverDetail(id) as never
    })
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')

    fireEvent.click(sb.getByRole('button', { name: '立即补建' }))

    expect(await screen.findByText('已开始补建')).toBeInTheDocument()
    expect(await sb.findByText(/^补建中/)).toBeInTheDocument()

    vi.mocked(backfillSeasonBinding).mockRejectedValue(new ApiError('正在补建，稍后再试', 1, 409))
    fireEvent.click(sb.getByRole('button', { name: '立即补建' }))
    expect(await screen.findByText('正在补建，稍后再试')).toBeInTheDocument()
    expect(sb.queryByRole('alert')).not.toBeInTheDocument()

    Object.assign(serverBinding(1), { running: false })
    await advance(1000)
    await waitFor(() => expect(sb.queryByText(/^补建中/)).not.toBeInTheDocument())
  })

  it('立即补建：文件夹当场完成，随即刷新剧详情', async () => {
    addBinding(folderSeasonBinding(2))
    vi.mocked(backfillSeasonBinding).mockImplementation(async (id) => serverDetail(id) as never)
    renderRoutes('/catalog/1/11')
    const sb = await card('来自新世界')
    const reloads = vi.mocked(getSeries).mock.calls.length

    fireEvent.click(sb.getByRole('button', { name: '立即补建' }))

    expect(await screen.findByText('已补建')).toBeInTheDocument()
    expect(backfillSeasonBinding).toHaveBeenCalledWith(2)
    await waitFor(() => expect(vi.mocked(getSeries).mock.calls.length).toBeGreaterThan(reloads))
  })

  it('关掉追更', async () => {
    vi.mocked(setFollow).mockImplementation(async (id, follow) => {
      Object.assign(serverBinding(id), { follow })
      return serverDetail(id) as never
    })
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人 第一季')

    fireEvent.click(sb.getByRole('switch', { name: '追更' }))

    await waitFor(() => expect(setFollow).toHaveBeenCalledWith(1, false))
    expect(await screen.findByText('已关闭追更')).toBeInTheDocument()
    await waitFor(() => expect(sb.getByRole('switch', { name: '追更' })).not.toBeChecked())
  })

  it.each([
    ['合集', 1, '星海旅人 第一季', '它建出的 1 个绑定会连同弹幕一起删除，无法恢复。'],
    [
      '文件夹',
      2,
      '来自新世界',
      '它建出的 1 个绑定会连同弹幕一起删除，上传的文件也一起删除，无法恢复。',
    ],
  ])('删除%s的季绑定：确认框写明建出的绑定一起删除', async (_, id, name, text) => {
    addBinding(folderSeasonBinding(2))
    vi.mocked(deleteSeasonBinding).mockResolvedValue(null)
    renderRoutes('/catalog/1/11')
    const sb = await card(name)

    fireEvent.click(sb.getByRole('button', { name: '删除' }))

    const dialog = within(await screen.findByRole('alertdialog'))
    expect(dialog.getByText(text)).toBeInTheDocument()
    expect(dialog.queryByRole('checkbox')).not.toBeInTheDocument()
    act(() => fireEvent.click(dialog.getByRole('button', { name: '删除' })))

    await waitFor(() => expect(deleteSeasonBinding).toHaveBeenCalledWith(id))
    expect(await screen.findByText('已删除季绑定')).toBeInTheDocument()
  })
})

/** 一季的存档：第 1、2 集各一个子目录（第 1 集有两份快照），一个认不出集号的 SP，一份说明 */
const archive = [
  '星海旅人/1/20120930.xml',
  '星海旅人/1/560685.xml',
  '星海旅人/2/20121007.xml',
  '星海旅人/SP/20121231.xml',
  '星海旅人/README.html',
].map(picked)

/** picked() 的文件内容的 SHA-256 */
const hash = '663ddd7a0a8adf83c191605743fa3a973fc2677385fd902292b30113f258e37e'

/** 存档的条目与哈希：后端按"文件夹名 / 条目名称"认集号，名称本身不带文件夹名 */
const archiveEntries = [
  { label: '1', sha256s: [hash, hash] },
  { label: '2', sha256s: [hash] },
  { label: 'SP', sha256s: [hash] },
]

/** 存档按条目派生的预览：第 1、2 集新建，SP 对不上；条目名称由后端给出 */
const archivePreview = {
  candidates: [
    candidate({
      collectionKind: null,
      title: '星海旅人',
      sourceUrl: null,
      sourceLabel: null,
      finished: false,
      numberedByRule: true,
      items: [
        item('1', 1, { status: 'alreadyBound' }),
        item('2', 2),
        item('SP', null, { status: 'unmatched', reason: '认不出集号' }),
      ],
      summary: { create: 2, replace: 0, remove: 0, keep: 0 },
    }),
  ],
}

/** 在 form 里选中文件夹（添加的表单要先取到默认的集号规则才显示选择框） */
async function pickFolder(form: ReturnType<typeof within>, files: File[]) {
  fireEvent.change(await form.findByLabelText('选择弹幕文件夹'), { target: { files } })
}

/** 在季面板上展开"上传弹幕文件夹" */
async function openFolderForm() {
  renderRoutes('/catalog/1/11')
  fireEvent.click(await screen.findByRole('button', { name: '上传弹幕文件夹' }))
  return within(await screen.findByRole('region', { name: '上传弹幕文件夹' }))
}

describe('上传弹幕文件夹', () => {
  it('选文件夹后按条目的哈希预览；上传时带上文件并显示进度，成功后收起、刷新剧详情', async () => {
    vi.mocked(previewNewFolder).mockResolvedValue(archivePreview)
    const done = deferred<never>()
    let progress: ((p: number) => void) | undefined
    vi.mocked(createFolder).mockImplementation((_, __, ___, onProgress) => {
      progress = onProgress
      return done.promise
    })
    const form = await openFolderForm()
    // 还没选文件夹：没有集号规则，不能上传
    expect(await form.findByLabelText('选择弹幕文件夹')).toBeInTheDocument()
    expect(form.queryByRole('group', { name: '集号规则' })).not.toBeInTheDocument()
    expect(await form.findByRole('button', { name: '上传' })).toBeDisabled()

    await pickFolder(form, archive)

    expect(
      await form.findByText('已选：星海旅人（3 个条目，忽略了 1 份不是 XML 的文件）'),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(previewNewFolder).toHaveBeenCalledWith(11, {
        folder: '星海旅人',
        entries: archiveEntries,
        episodeRule: defaultEpisodeRule,
        mappingFrom: 1,
        mappingTo: 1,
      }),
    )
    // 集号对应预填 1 = 1
    expect(form.getByRole('textbox', { name: '第几集' })).toHaveValue('1')
    expect(form.getByRole('group', { name: '集号规则' })).toBeInTheDocument()
    await form.findByRole('region', { name: '预览' })
    expect(previewRows(form)).toEqual([
      ['1', '1', '第 1 集 · 跳过：已有同源绑定'],
      ['2', '2', '第 2 集 · 新建'],
      ['—', 'SP', '对不上（认不出集号）'],
    ])
    await waitFor(() => expect(form.getByRole('button', { name: '上传' })).toBeEnabled())

    fireEvent.click(form.getByRole('button', { name: '上传' }))

    await waitFor(() => expect(createFolder).toHaveBeenCalledOnce())
    const [seasonId, files, rules] = vi.mocked(createFolder).mock.calls[0]!
    expect(seasonId).toBe(11)
    expect(files.map((f) => f.webkitRelativePath)).toEqual([
      '星海旅人/1/560685.xml',
      '星海旅人/1/20120930.xml',
      '星海旅人/2/20121007.xml',
      '星海旅人/SP/20121231.xml',
    ])
    expect(rules).toEqual({ episodeRule: defaultEpisodeRule, mappingFrom: 1, mappingTo: 1 })
    act(() => progress!(0.5))
    expect(await form.findByText('上传中 50%')).toBeInTheDocument()
    expect(form.getByRole('progressbar', { name: '上传进度' })).toBeInTheDocument()
    // 上传进行中不能取消
    expect(form.getByRole('button', { name: '取消' })).toBeDisabled()
    act(() => progress!(1))
    expect(await form.findByText(/^正在保存/)).toBeInTheDocument()

    const reloads = vi.mocked(getSeries).mock.calls.length
    const sb = addBinding(folderSeasonBinding(2, { title: '星海旅人', bindingCount: 1 }))
    done.resolve(serverDetail(sb.id) as never)

    expect(await screen.findByText('已上传「星海旅人」，建出 1 个绑定')).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.queryByRole('region', { name: '上传弹幕文件夹' })).not.toBeInTheDocument(),
    )
    await waitFor(() => expect(vi.mocked(getSeries).mock.calls.length).toBeGreaterThan(reloads))
    expect(await card('星海旅人')).toBeTruthy()
  })

  it('文件夹的结构不对时直接提示，不预览', async () => {
    const form = await openFolderForm()

    await pickFolder(form, [picked('10月/星海旅人/1/20120930.xml')])

    expect(
      await form.findByText(
        '目录最多两层（文件夹 / 子目录 / 文件），这份文件超出了：10月/星海旅人/1/20120930.xml',
      ),
    ).toBeInTheDocument()
    expect(previewNewFolder).not.toHaveBeenCalled()
  })

  it('上传失败：提示留在表单里，预览不变，可以再点一次', async () => {
    vi.mocked(previewNewFolder).mockResolvedValue(archivePreview)
    vi.mocked(createFolder).mockRejectedValue(
      new ApiError('认不出弹幕文件：星海旅人/1/560685.xml', 1, 422),
    )
    const form = await openFolderForm()
    await pickFolder(form, archive)
    await form.findByRole('region', { name: '预览' })
    await waitFor(() => expect(form.getByRole('button', { name: '上传' })).toBeEnabled())

    fireEvent.click(form.getByRole('button', { name: '上传' }))

    expect(await form.findByRole('alert')).toHaveTextContent(
      '认不出弹幕文件：星海旅人/1/560685.xml',
    )
    expect(form.getByRole('table', { name: '预览表' })).toBeInTheDocument()
    fireEvent.click(form.getByRole('button', { name: '上传' }))
    await waitFor(() => expect(createFolder).toHaveBeenCalledTimes(2))
  })
})

describe('编辑文件夹的季绑定', () => {
  it('打开就按保存的条目预览，不换文件夹时只发集号规则、集号对应；换了文件夹带上文件夹和条目预览，保存时上传', async () => {
    addBinding(folderSeasonBinding(2, { title: '星海旅人' }))
    vi.mocked(previewFolderEdit).mockResolvedValue({
      candidates: [{ ...archivePreview.candidates[0]!, current: true }],
    })
    vi.mocked(saveFolder).mockImplementation(async (id) => serverDetail(id) as never)
    renderRoutes('/catalog/1/11')
    const sb = await card('星海旅人')

    fireEvent.click(sb.getByRole('button', { name: '编辑' }))

    const form = within(sb.getByRole('region', { name: '编辑「星海旅人」' }))
    expect(form.getByText('换文件夹（不换就只改集号规则、集号对应）')).toBeInTheDocument()
    await waitFor(() =>
      expect(previewFolderEdit).toHaveBeenCalledWith(2, {
        episodePatterns: defaultPatterns,
        mappingFrom: 1,
        mappingTo: 1,
      }),
    )
    await form.findByRole('region', { name: '预览' })
    await waitFor(() => expect(form.getByRole('button', { name: '保存' })).toBeEnabled())

    // 没改也能保存（保存时删掉没有条目的槽位）
    fireEvent.click(form.getByRole('button', { name: '保存' }))
    await waitFor(() =>
      expect(saveFolder).toHaveBeenCalledWith(
        2,
        null,
        { episodePatterns: defaultPatterns, mappingFrom: 1, mappingTo: 1 },
        expect.any(Function),
      ),
    )
    expect(await screen.findByText('已保存「星海旅人」')).toBeInTheDocument()
    await waitFor(() =>
      expect(sb.queryByRole('region', { name: '编辑「星海旅人」' })).not.toBeInTheDocument(),
    )

    // 再编辑一次，换文件夹
    fireEvent.click(sb.getByRole('button', { name: '编辑' }))
    const again = within(sb.getByRole('region', { name: '编辑「星海旅人」' }))
    await again.findByRole('region', { name: '预览' })
    await pickFolder(
      again,
      archive.map((f) => picked(f.webkitRelativePath.replace('星海旅人', '星海旅人 v2'))),
    )

    await waitFor(() =>
      expect(previewFolderEdit).toHaveBeenLastCalledWith(2, {
        folder: '星海旅人 v2',
        entries: archiveEntries,
        episodePatterns: defaultPatterns,
        mappingFrom: 1,
        mappingTo: 1,
      }),
    )
    await waitFor(() => expect(again.getByRole('button', { name: '保存' })).toBeEnabled())
    fireEvent.click(again.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(saveFolder).toHaveBeenCalledTimes(2))
    const [, files] = vi.mocked(saveFolder).mock.calls[1]!
    expect(files).toHaveLength(4)
    expect(files?.[0]?.webkitRelativePath).toBe('星海旅人 v2/1/560685.xml')
  })
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

  fireEvent.click(list.getByRole('button', { name: '筛选条件' }))
  const toggle = await screen.findByRole('checkbox', { name: '只看追更中' })
  fireEvent.click(toggle)

  expect(toggle).toBeChecked()
  expect(list.getByText('1 部')).toBeInTheDocument()
  expect(list.queryByRole('link', { name: /长夜灯塔/ })).not.toBeInTheDocument()

  fireEvent.click(toggle)
  expect(list.getByRole('link', { name: /长夜灯塔/ })).toBeInTheDocument()
})
