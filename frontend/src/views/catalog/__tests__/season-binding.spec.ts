import { describe, expect, it } from 'vitest'

import { folderSeasonBinding, seasonBinding } from '@/__tests__/utils'
import type { Candidate, Item } from '@/api/season-bindings'
import {
  candidateText,
  itemStatusText,
  parseMappingNumber,
  previewStatusText,
  seasonBindingTag,
  summaryTags,
} from '../season-binding'

describe('parseMappingNumber', () => {
  it.each([
    ['0', 0],
    [' 14 ', 14],
    ['999999999', 999999999],
    ['', null],
    ['-1', null],
    ['1.5', null],
    ['1e2', null],
    ['1000000000', null],
    ['十四', null],
  ])('%j', (text, want) => {
    expect(parseMappingNumber(text)).toBe(want)
  })
})

describe('candidateText', () => {
  const candidate = (collectionKind: string): Candidate => ({
    collectionKind,
    title: '标题',
    sourceUrl: 'https://example.com',
    sourceLabel: '标签',
    finished: false,
    numberedByRule: false,
    current: false,
    items: [derived({}), derived({})],
    removed: [],
    summary: { create: 2, replace: 0, remove: 0, keep: 0 },
  })
  it.each([
    ['multiPage', '这个稿件《标题》的 2 个分 P'],
    ['ugcSeason', '它所在的合集《标题》共 2 条'],
    ['bangumi', '标签《标题》共 2 条'],
  ])('%s', (kind, want) => {
    expect(candidateText(candidate(kind))).toBe(want)
  })
})

/** 目录页的地址，带着分类的查询参数 */
const tvPath = (...ids: number[]) => `/catalog/${ids.join('/')}?type=tv`

describe('seasonBindingTag', () => {
  // 剧 1 的季 11
  const season = {
    id: 11,
    seasonBindings: [
      seasonBinding(1, { title: '星海旅人 第一季' }),
      seasonBinding(2, { title: '', sourceLabel: 'B 站投稿合集 123' }),
      folderSeasonBinding(3, { title: '来自新世界' }),
    ],
  }
  it.each([
    [null, null],
    [
      1,
      {
        text: '季绑定 · 星海旅人 第一季',
        title: '由合集的季绑定「星海旅人 第一季」按集号对应建出',
        to: '/catalog/1/11',
      },
    ],
    // 合集标题为空时用合集的标签
    [
      2,
      {
        text: '季绑定 · B 站投稿合集 123',
        title: '由合集的季绑定「B 站投稿合集 123」按集号对应建出',
        to: '/catalog/1/11',
      },
    ],
    [
      3,
      {
        text: '季绑定 · 来自新世界',
        title: '由文件夹的季绑定「来自新世界」建出',
        to: '/catalog/1/11',
      },
    ],
    // 找不到对应的季绑定
    [9, { text: '季绑定', title: '由季绑定建出', to: '/catalog/1/11' }],
  ])('%s', (id, want) => {
    expect(seasonBindingTag(id, season, 1)).toEqual(want)
  })

  it('季面板的路径用给的 path 拼出，例如带上地址栏的查询参数', () => {
    expect(seasonBindingTag(1, season, 1, tvPath)?.to).toBe('/catalog/1/11?type=tv')
  })
})

/** 一个条目派生的结果，默认是对到第 3 集、新建的 */
const derived = (patch: Partial<Item>): Item => ({
  label: '条目',
  number: 3,
  episodeNumber: 3,
  status: 'create',
  reason: null,
  replaces: null,
  ...patch,
})

describe('itemStatusText', () => {
  const replaces = { episodeNumber: 3, bindingId: 9, title: '旧稿件', dead: false }
  it.each([
    ['collection', derived({ status: 'keep' }), '第 3 集 · 已建绑定'],
    ['collection', derived({ status: 'create' }), '第 3 集 · 待补建'],
    ['collection', derived({ status: 'replace', replaces }), '第 3 集 · 待换新稿件（原：旧稿件）'],
    ['folder', derived({ status: 'replace', replaces }), '第 3 集 · 待替换（原：旧稿件）'],
    ['collection', derived({ status: 'alreadyBound' }), '第 3 集 · 已有同源绑定'],
    [
      'collection',
      derived({ status: 'unmatched', number: null, episodeNumber: null, reason: '集号重复' }),
      '对不上（集号重复）',
    ],
    ['folder', derived({ status: 'beforeStart', number: 0, episodeNumber: null }), '在起点之前'],
    [
      'collection',
      derived({ status: 'noEpisode', episodeNumber: 13 }),
      '第 13 集 · 目录里没有这一集',
    ],
  ] as const)('%s %j', (kind, d, want) => {
    expect(itemStatusText(d, kind)).toBe(want)
  })
})

describe('previewStatusText', () => {
  const replaces = { episodeNumber: 3, bindingId: 9, title: '旧稿件', dead: false }
  it.each([
    [derived({ status: 'keep' }), '第 3 集 · 保留'],
    [derived({ status: 'create' }), '第 3 集 · 新建'],
    [derived({ status: 'replace', replaces }), '第 3 集 · 替换（原：旧稿件）'],
    [derived({ status: 'alreadyBound' }), '第 3 集 · 跳过：已有同源绑定'],
    [
      derived({ status: 'unmatched', number: null, episodeNumber: null, reason: '认不出集号' }),
      '对不上（认不出集号）',
    ],
    [derived({ status: 'beforeStart', number: 0, episodeNumber: null }), '在起点之前'],
    [derived({ status: 'noEpisode', episodeNumber: 13 }), '第 13 集 · 目录里没有这一集'],
  ])('%j', (d, want) => {
    expect(previewStatusText(d)).toBe(want)
  })
})

describe('summaryTags', () => {
  it('依次是新建、替换、删除、保留，删除大于 0 时标红', () => {
    expect(summaryTags({ create: 2, replace: 1, remove: 3, keep: 10 })).toEqual([
      { text: '新建 2', destructive: false },
      { text: '替换 1', destructive: false },
      { text: '删除 3', destructive: true },
      { text: '保留 10', destructive: false },
    ])
  })

  it('没有删除时不标红', () => {
    expect(summaryTags({ create: 0, replace: 0, remove: 0, keep: 4 })).toEqual([
      { text: '新建 0', destructive: false },
      { text: '替换 0', destructive: false },
      { text: '删除 0', destructive: false },
      { text: '保留 4', destructive: false },
    ])
  })
})
