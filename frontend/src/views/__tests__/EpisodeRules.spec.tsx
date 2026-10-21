import { useState } from 'react'
import { beforeEach, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'

import {
  defaultEpisodeRule,
  episodeRuleDefaults,
  legacyEpisodeRule,
  newQueryClient,
} from '@/__tests__/utils'
import {
  getDefaultEpisodePatterns,
  renderEpisodeTemplate,
  type EpisodeRuleConfig,
  type Item,
} from '@/api/season-bindings'
import EpisodeRuleInput from '@/views/catalog/EpisodeRuleInput'
import { ItemTable } from '@/views/catalog/PreviewTable'

vi.mock('@/api/season-bindings')

beforeEach(() => {
  vi.mocked(getDefaultEpisodePatterns).mockResolvedValue(episodeRuleDefaults)
  vi.mocked(renderEpisodeTemplate).mockImplementation(async ({ adjustment }) => ({
    rule: { pattern: '^第二季(?P<episode>\\d+)$', adjustment },
    example: '第二季01',
  }))
})

function Editor({ initial }: { initial: EpisodeRuleConfig }) {
  const [rule, setRule] = useState(structuredClone(initial))
  return (
    <>
      <EpisodeRuleInput value={rule} onChange={setRule} />
      <output data-testid="draft">{JSON.stringify(rule)}</output>
    </>
  )
}

function editor(initial = defaultEpisodeRule) {
  render(
    <QueryClientProvider client={newQueryClient()}>
      <Editor initial={initial} />
    </QueryClientProvider>,
  )
}

const draft = () => JSON.parse(screen.getByTestId('draft').textContent!) as EpisodeRuleConfig

it('模板由后端生成：指定第二季加12，前置覆盖和替换都使用返回的规则', async () => {
  editor()
  await screen.findByRole('option', { name: '季号＋数字' })
  fireEvent.click(screen.getByText('使用模板'))
  fireEvent.change(screen.getByLabelText('集号模板'), { target: { value: 'seasonNumber' } })
  fireEvent.change(screen.getByLabelText('模板来源季'), { target: { value: '2' } })
  fireEvent.change(screen.getByLabelText('模板集号加减'), { target: { value: '12' } })
  await waitFor(() =>
    expect(renderEpisodeTemplate).toHaveBeenLastCalledWith({
      templateId: 'seasonNumber',
      season: 2,
      adjustment: 12,
    }),
  )
  const add = screen.getByRole('button', { name: '添加覆盖规则' })
  await waitFor(() => expect(add).toBeEnabled())
  fireEvent.click(add)
  expect(draft().rules).toEqual([
    { pattern: '^第二季(?P<episode>\\d+)$', adjustment: 12 },
    ...defaultEpisodeRule.rules,
  ])
  fireEvent.click(screen.getByRole('button', { name: '仅使用此规则' }))
  expect(draft().rules).toEqual([{ pattern: '^第二季(?P<episode>\\d+)$', adjustment: 12 }])
  fireEvent.change(screen.getByLabelText('模板集号加减'), { target: { value: '1.5' } })
  expect(screen.getByRole('button', { name: '仅使用此规则' })).toBeDisabled()
})

it('仅正片开关保留自定义排除；匹配范围、加减和高级正则都进入草稿', async () => {
  editor({
    ...defaultEpisodeRule,
    excludes: [...defaultEpisodeRule.excludes, { pattern: '重制', kind: 'custom' }],
  })
  const regular = await screen.findByRole('checkbox', { name: '仅正片' })
  await waitFor(() => expect(regular).toBeEnabled())
  fireEvent.click(regular)
  expect(draft().excludes).toEqual([{ pattern: '重制', kind: 'custom' }])
  fireEvent.click(regular)
  expect(draft().excludes).toEqual([
    { pattern: '重制', kind: 'custom' },
    ...defaultEpisodeRule.excludes,
  ])
  fireEvent.change(screen.getByLabelText('匹配范围'), { target: { value: 'full' } })
  fireEvent.change(screen.getByLabelText('第 1 条集号加减'), { target: { value: '-12' } })
  fireEvent.click(screen.getByText('高级正则与排除规则'))
  fireEvent.change(screen.getByLabelText('集号规则第 1 条'), { target: { value: '第(\\d+)话' } })
  expect(draft().scope).toBe('full')
  expect(draft().rules[0]).toEqual({ pattern: '第(\\d+)话', adjustment: -12 })
})

it('旧版主动升级：打开时原配置保留，点击后只更新草稿', async () => {
  editor(legacyEpisodeRule)
  expect(screen.getByText(/旧版规则/)).toBeInTheDocument()
  expect(draft()).toEqual(legacyEpisodeRule)
  const upgrade = screen.getByRole('button', { name: '使用新版模板' })
  await waitFor(() => expect(upgrade).toBeEnabled())
  fireEvent.click(upgrade)
  expect(draft()).toEqual(defaultEpisodeRule)
  expect(legacyEpisodeRule.version).toBe(1)
})

it('诊断显示捕获、来源季和重复号，并能定位其他冲突行', () => {
  const scroll = vi.fn<() => void>()
  Object.defineProperty(Element.prototype, 'scrollIntoView', { configurable: true, value: scroll })
  const items: Item[] = ['第一季01', '第二季01'].map((label) => ({
    label,
    number: null,
    episodeNumber: null,
    status: 'unmatched',
    reason: '集号重复',
    reasonCode: 'duplicateNumber',
    replaces: null,
    trace: {
      version: 2,
      scope: 'entry',
      scopeLabel: '分 P 标题',
      input: label,
      normalized: true,
      ruleIndex: 1,
      captured: '01',
      season: label.slice(0, 3),
      rawNumber: 1,
      adjustment: 0,
      adjustedNumber: 1,
      conflicts: [
        { row: 1, label: '第一季01' },
        { row: 2, label: '第二季01' },
      ],
    },
  }))
  render(
    <ItemTable label="诊断" items={items} statusHead="状态" renderStatus={() => <td>重复</td>} />,
  )
  const rows = screen.getAllByRole('row').slice(1)
  fireEvent.click(within(rows[0]!).getByText('识别诊断'))
  expect(within(rows[0]!).getByText('捕获内容：01')).toBeInTheDocument()
  expect(within(rows[0]!).getByText('来源季标记：第一季')).toBeInTheDocument()
  fireEvent.click(within(rows[0]!).getByRole('button', { name: /定位冲突行 2/ }))
  expect(scroll).toHaveBeenCalledOnce()
  expect(rows[1]).toHaveFocus()
})
