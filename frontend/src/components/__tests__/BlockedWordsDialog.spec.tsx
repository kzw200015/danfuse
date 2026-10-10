import { beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'

import {
  createBlockedWord,
  deleteBlockedWord,
  listBlockedWords,
  type BlockedWord,
} from '@/api/blocked-words'
import { ApiError } from '@/api/request'
import { listSeries } from '@/api/series'
import { mockRootLayout, renderRoutes } from '@/__tests__/utils'

vi.mock('@/api/blocked-words')
vi.mock('@/api/series')
vi.mock('@/api/settings')
vi.mock('@/api/sync')

function word(id: number, kind: BlockedWord['kind'], pattern: string): BlockedWord {
  return { id, kind, pattern, createdAt: '2026-10-10T08:00:00Z' }
}

/** 服务端的屏蔽词，新加的在前；接口按它返回，增删也改它 */
let words: BlockedWord[]

beforeEach(() => {
  vi.mocked(listSeries).mockResolvedValue([])
  mockRootLayout()
  words = [word(2, 'regex', '^\\d+$'), word(1, 'keyword', '剧透')]
  vi.mocked(listBlockedWords).mockImplementation(async () => structuredClone(words))
  vi.mocked(createBlockedWord).mockImplementation(async (kind, pattern) => {
    const created = word(Math.max(0, ...words.map((w) => w.id)) + 1, kind, pattern.trim())
    words = [created, ...words]
    return created
  })
  vi.mocked(deleteBlockedWord).mockImplementation(async (id) => {
    words = words.filter((w) => w.id !== id)
    return null
  })
})

/** 打开设置弹出层，点屏蔽词的"管理"，返回管理屏蔽词的对话框 */
async function openBlockedWords() {
  renderRoutes('/catalog')
  fireEvent.click(await screen.findByRole('button', { name: '设置' }))
  const popover = await screen.findByRole('dialog')
  expect(await within(popover).findByText(/^2 条/)).toBeInTheDocument()
  fireEvent.click(within(popover).getByRole('button', { name: '管理' }))
  return screen.findByRole('dialog', { name: '屏蔽词' })
}

/** 列表里每条屏蔽词写成"类型 内容" */
function listed(dialog: HTMLElement) {
  return within(dialog)
    .queryAllByRole('listitem')
    .map((li) => li.textContent)
}

describe('屏蔽词', () => {
  it('设置弹出层显示条数，"管理"打开对话框，新加的在前', async () => {
    const dialog = await openBlockedWords()

    await waitFor(() => expect(listed(dialog)).toEqual(['正则^\\d+$', '关键词剧透']))
    // 打开对话框时弹出层随即关闭
    expect(screen.getAllByRole('dialog')).toHaveLength(1)
  })

  it('选类型添加，成功后清空输入框、列表刷新', async () => {
    const dialog = await openBlockedWords()

    fireEvent.click(within(dialog).getByRole('button', { name: '正则' }))
    const input = within(dialog).getByRole('textbox', { name: '屏蔽词' })
    fireEvent.change(input, { target: { value: '(?i)awsl' } })
    fireEvent.submit(input)

    expect(await screen.findByText('已添加屏蔽词「(?i)awsl」')).toBeInTheDocument()
    expect(createBlockedWord).toHaveBeenCalledWith('regex', '(?i)awsl')
    expect(input).toHaveValue('')
    await waitFor(() => expect(listed(dialog)[0]).toBe('正则(?i)awsl'))
  })

  it('添加失败时提示留在输入框下方', async () => {
    vi.mocked(createBlockedWord).mockRejectedValue(new ApiError('已有相同的屏蔽词', 1, 409))
    const dialog = await openBlockedWords()

    const input = within(dialog).getByRole('textbox', { name: '屏蔽词' })
    fireEvent.change(input, { target: { value: 'Ｊ U 透' } })
    fireEvent.click(within(dialog).getByRole('button', { name: '添加' }))

    expect(await within(dialog).findByRole('alert')).toHaveTextContent('已有相同的屏蔽词')
    expect(createBlockedWord).toHaveBeenCalledWith('keyword', 'Ｊ U 透')
    expect(input).toHaveValue('Ｊ U 透')
  })

  it('直接删除，不再确认', async () => {
    const dialog = await openBlockedWords()

    fireEvent.click(await within(dialog).findByRole('button', { name: '删除屏蔽词 剧透' }))

    expect(await screen.findByText('已删除屏蔽词「剧透」')).toBeInTheDocument()
    expect(deleteBlockedWord).toHaveBeenCalledWith(1)
    await waitFor(() => expect(listed(dialog)).toEqual(['正则^\\d+$']))
  })
})
