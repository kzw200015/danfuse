import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'

import {
  binding,
  fileBinding,
  lighthouse,
  mockCatalog,
  mockRootLayout,
  renderRoutes,
} from '@/__tests__/utils'
import { createSeasonFileBindings, previewSeasonFileBindings } from '@/api/bindings'
import { ApiError } from '@/api/request'
import { getDefaultEpisodePatterns } from '@/api/season-bindings'
import { getSeries, type SeriesDetail } from '@/api/series'

vi.mock('@/api/bindings')
vi.mock('@/api/season-bindings')
vi.mock('@/api/series')
// 根布局会取最近一次同步和设置
vi.mock('@/api/settings')
vi.mock('@/api/sync')

/** 默认的集号规则（假的，只要是两条） */
const defaultPatterns = ['第(\\d+)集', '/ (\\d+)$']

/** 星海旅人的第 1 季（季 11）：第 1 集（集 110）没有绑定，第 2 集（集 111）有文件绑定，第 3 集（集 112）有链接绑定 */
function starVoyager(): SeriesDetail {
  return {
    id: 1,
    type: 'tv',
    title: '星海旅人',
    originalTitle: null,
    year: 2019,
    tmdbId: null,
    posterImageId: null,
    seasons: [
      {
        id: 11,
        number: 1,
        title: null,
        seasonBindings: [],
        episodes: [
          { id: 110, number: 1, title: '启程', duration: 1420, bindings: [] },
          { id: 111, number: 2, title: null, duration: 1420, bindings: [fileBinding(5)] },
          { id: 112, number: 3, title: null, duration: 1420, bindings: [binding(6)] },
        ],
      },
    ],
  }
}

let all: SeriesDetail[]

beforeEach(() => {
  all = [starVoyager(), lighthouse()]
  mockRootLayout()
  mockCatalog(() => all)
  vi.mocked(getDefaultEpisodePatterns).mockResolvedValue({ episodePatterns: defaultPatterns })
  // 假的预览：条目名称结尾是数字的认出集号，其余认不出
  vi.mocked(previewSeasonFileBindings).mockImplementation(async (_, labels) => ({
    items: labels.map((label) => {
      const m = /(\d+)$/.exec(label)
      return m
        ? { label, number: Number(m[1]), reason: null }
        : { label, number: null, reason: '认不出集号' }
    }),
  }))
})

/** 目录选择选出的一份文件：jsdom 不实现 webkitRelativePath，手动补上 */
function picked(path: string) {
  const file = new File(['<i></i>'], path.split('/').at(-1)!, { type: 'text/xml' })
  Object.defineProperty(file, 'webkitRelativePath', { value: path })
  return file
}

/** 一季的存档：第 1～4 集各一个子目录（第 1 集有两份快照），一个认不出集号的 SP，一份说明 */
const archive = [
  '星海旅人/1/20120930.xml',
  '星海旅人/1/560685.xml',
  '星海旅人/2/20121007.xml',
  '星海旅人/3/20121014.xml',
  '星海旅人/4/20121021.xml',
  '星海旅人/SP/20121231.xml',
  '星海旅人/README.html',
].map(picked)

const labels = ['星海旅人 / 1', '星海旅人 / 2', '星海旅人 / 3', '星海旅人 / 4', '星海旅人 / SP']

/** 打开季面板上的对话框，选中文件夹 */
async function pickFolder(files: File[]) {
  renderRoutes('/catalog/1/11')
  fireEvent.click(await screen.findByRole('button', { name: '上传弹幕文件夹' }))
  const dialog = within(await screen.findByRole('dialog', { name: '上传弹幕文件夹' }))
  fireEvent.change(dialog.getByLabelText('选择弹幕文件夹'), { target: { files } })
  return dialog
}

/** 预览表格各行：条目、勾选状态（disabled 表示不能勾）、对到本地 */
function rows(dialog: ReturnType<typeof within>) {
  return within(dialog.getByRole('table', { name: '上传预览' }))
    .getAllByRole('row')
    .slice(1)
    .map((row) => {
      const box = within(row).getByRole('checkbox')
      const cells = within(row).getAllByRole('cell')
      return [
        cells[2]!.textContent,
        box.getAttribute('aria-disabled') === 'true'
          ? 'disabled'
          : box.getAttribute('aria-checked') === 'true',
        cells[3]!.textContent,
      ]
    })
}

/** 一个控制完成时机的 Promise */
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}

describe('按季上传', () => {
  it('选文件夹后预览：对到已有的集的默认勾上，已有文件绑定的不勾，对不上的不能勾；确认后上传勾选的条目', async () => {
    const done = deferred<{ bindings: number; added: number }>()
    let progress: ((p: number) => void) | undefined
    vi.mocked(createSeasonFileBindings).mockImplementation((_, __, onProgress) => {
      progress = onProgress
      return done.promise
    })

    const dialog = await pickFolder(archive)

    await waitFor(() =>
      expect(previewSeasonFileBindings).toHaveBeenCalledWith(11, labels, defaultPatterns),
    )
    expect(await dialog.findByText('5 个条目，忽略了 1 份不是 XML 的文件')).toBeInTheDocument()
    // 集号对应预填 1 = 1，同季绑定
    expect(dialog.getByRole('textbox', { name: '第几集' })).toHaveValue('1')
    expect(dialog.getByRole('textbox', { name: '本地第几集' })).toHaveValue('1')
    expect(rows(dialog)).toEqual([
      ['星海旅人 / 1', true, '第 1 集'],
      ['星海旅人 / 2', false, '第 2 集（已有 1 个绑定） · 已有文件绑定'],
      ['星海旅人 / 3', true, '第 3 集（已有 1 个绑定）'],
      ['星海旅人 / 4', 'disabled', '目录里还没有第 4 集'],
      ['星海旅人 / SP', 'disabled', '对不上：认不出集号'],
    ])

    // 真想再传一份时手动勾上，不想要的去掉
    fireEvent.click(dialog.getByRole('checkbox', { name: '上传 星海旅人 / 2' }))
    fireEvent.click(dialog.getByRole('checkbox', { name: '上传 星海旅人 / 3' }))
    fireEvent.click(dialog.getByRole('button', { name: '上传 2 个条目' }))

    await waitFor(() => expect(createSeasonFileBindings).toHaveBeenCalledOnce())
    const [seasonId, upload] = vi.mocked(createSeasonFileBindings).mock.calls[0]!
    expect(seasonId).toBe(11)
    // 只有勾选的条目的文件，路径与文件一一对应
    const paths = ['星海旅人/1/560685.xml', '星海旅人/1/20120930.xml', '星海旅人/2/20121007.xml']
    expect(upload.paths).toEqual(paths)
    expect(upload.files.map((f) => f.webkitRelativePath)).toEqual(paths)
    expect(upload.targets).toEqual([
      { label: '星海旅人 / 1', episodeId: 110 },
      { label: '星海旅人 / 2', episodeId: 111 },
    ])
    progress!(0.5)
    expect(await dialog.findByText('上传中 50%')).toBeInTheDocument()
    expect(dialog.getByRole('progressbar', { name: '上传进度' })).toBeInTheDocument()
    progress!(1)
    expect(await dialog.findByText(/^正在保存/)).toBeInTheDocument()

    const reloads = vi.mocked(getSeries).mock.calls.length
    done.resolve({ bindings: 2, added: 1234 })

    expect(await screen.findByText('已建出 2 个绑定，共 1,234 条弹幕')).toBeInTheDocument()
    await waitFor(() =>
      expect(screen.queryByRole('dialog', { name: '上传弹幕文件夹' })).not.toBeInTheDocument(),
    )
    await waitFor(() => expect(vi.mocked(getSeries).mock.calls.length).toBeGreaterThan(reloads))
  })

  it('上传失败：提示留在对话框里，预览和勾选保持原样，可以直接再点一次', async () => {
    vi.mocked(createSeasonFileBindings).mockRejectedValue(
      new ApiError('认不出弹幕文件：星海旅人/1/560685.xml', 1, 422),
    )
    const dialog = await pickFolder(archive)
    await dialog.findByRole('table', { name: '上传预览' })
    const before = rows(dialog)

    fireEvent.click(dialog.getByRole('button', { name: '上传 2 个条目' }))

    expect(await dialog.findByText('认不出弹幕文件：星海旅人/1/560685.xml')).toBeInTheDocument()
    expect(rows(dialog)).toEqual(before)
    fireEvent.click(dialog.getByRole('button', { name: '上传 2 个条目' }))
    await waitFor(() => expect(createSeasonFileBindings).toHaveBeenCalledTimes(2))
  })

  it('文件夹的结构不对时直接提示，不预览', async () => {
    const dialog = await pickFolder([picked('10月/星海旅人/1/20120930.xml')])

    expect(
      await dialog.findByText(
        '目录最多两层（文件夹 / 子目录 / 文件），这份文件超出了：10月/星海旅人/1/20120930.xml',
      ),
    ).toBeInTheDocument()
    expect(previewSeasonFileBindings).not.toHaveBeenCalled()
  })

  it('改了集号规则要重新预览才能上传，表格按新的预览刷新', async () => {
    const dialog = await pickFolder(archive)
    await dialog.findByRole('table', { name: '上传预览' })

    fireEvent.change(dialog.getByRole('textbox', { name: '集号规则第 2 条' }), {
      target: { value: '' },
    })
    expect(dialog.getByRole('button', { name: '上传 2 个条目' })).toBeDisabled()
    vi.mocked(previewSeasonFileBindings).mockResolvedValueOnce({
      items: labels.map((label) => ({ label, number: null, reason: '认不出集号' })),
    })
    fireEvent.click(dialog.getByRole('button', { name: '重新预览' }))

    await waitFor(() =>
      expect(previewSeasonFileBindings).toHaveBeenLastCalledWith(11, labels, [defaultPatterns[0]]),
    )
    await waitFor(() =>
      expect(rows(dialog).map((r) => r[2])).toEqual(labels.map(() => '对不上：认不出集号')),
    )
    expect(dialog.getByRole('button', { name: '上传 0 个条目' })).toBeDisabled()
  })
})
