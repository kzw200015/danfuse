import { describe, expect, it } from 'vitest'

import { defaultPatterns, folderSeasonBinding, seasonBinding } from '@/__tests__/utils'
import type { Binding } from '@/api/bindings'
import type {
  CollectionCandidate,
  Item,
  PreviewItem,
  SeasonBindingItem,
} from '@/api/season-bindings'
import type { Episode } from '@/api/series'
import {
  candidateText,
  itemStateText,
  itemStatusText,
  parseMappingNumber,
  previewTarget,
  previewTargetText,
  seasonBindingPatch,
  seasonBindingTag,
  summaryTags,
} from '../season-binding'

function item(number: number | null, reason: string | null = null): PreviewItem {
  return { label: '条目', number, reason }
}

/** 本季：第 1、2 集，第 2 集已有两个绑定 */
const episodes: Episode[] = [
  { id: 110, number: 1, title: null, duration: null, bindings: [] },
  {
    id: 111,
    number: 2,
    title: null,
    duration: null,
    bindings: [{ id: 1 } as Binding, { id: 2 } as Binding],
  },
]

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

describe('previewTarget', () => {
  it.each([
    ['同号对应', item(1), { from: 1, to: 1 }, '第 1 集'],
    ['对到的集已有绑定', item(2), { from: 1, to: 1 }, '第 2 集（已有 2 个绑定）'],
    ['B 站从 14 开始、本地从 1 开始', item(15), { from: 14, to: 1 }, '第 2 集（已有 2 个绑定）'],
    ['在起点之前', item(13), { from: 14, to: 1 }, '在起点之前'],
    ['目录里还没有这一集', item(3), { from: 1, to: 1 }, '目录里还没有第 3 集'],
    ['本地从后面接上', item(1), { from: 1, to: 13 }, '目录里还没有第 13 集'],
    [
      '对不上：原因由后端给出',
      item(null, '集号「SP」不是整数'),
      { from: 1, to: 1 },
      '对不上：集号「SP」不是整数',
    ],
    ['集号重复', item(null, '集号重复'), { from: 0, to: 0 }, '对不上：集号重复'],
  ])('%s', (_, previewItem, mapping, want) => {
    expect(previewTargetText(previewTarget(previewItem, mapping, episodes))).toBe(want)
  })

  it('对到的集给出这一集本身', () => {
    expect(previewTarget(item(15), { from: 14, to: 1 }, episodes)).toEqual({
      kind: 'episode',
      episode: episodes[1],
    })
  })
})

/** 一个条目，默认是待补建到第 3 集的 */
const row = (patch: Partial<SeasonBindingItem>): SeasonBindingItem => ({
  label: '条目',
  number: 3,
  state: 'pending',
  reason: null,
  episodeNumber: 3,
  lastErrorAt: null,
  ...patch,
})

describe('itemStateText', () => {
  it.each([
    [row({ state: 'bound' }), '已建绑定：第 3 集'],
    [row({ state: 'alreadyBound' }), '集上已有这个弹幕源：第 3 集'],
    [row({ state: 'bindingDeleted' }), '绑定已被删除（第 3 集）'],
    [
      row({ state: 'unmatched', number: null, episodeNumber: null, reason: '集号重复' }),
      '对不上：集号重复',
    ],
    [row({ state: 'beforeStart', episodeNumber: null }), '在起点之前'],
    [row({ state: 'waitingEpisode', episodeNumber: 5 }), '等待目录里出现第 5 集'],
    [row({ state: 'pending' }), '待补建：第 3 集'],
    [row({ state: 'failed', reason: 'B 站接口异常' }), '最近一次失败：B 站接口异常'],
  ])('%j', (entry, want) => {
    expect(itemStateText(entry)).toBe(want)
  })
})

describe('candidateText', () => {
  const candidate = (kind: string): CollectionCandidate => ({
    kind,
    title: '标题',
    sourceUrl: 'https://example.com',
    sourceLabel: '标签',
    finished: false,
    numberedByRule: false,
    items: [item(1), item(2)],
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

describe('seasonBindingPatch', () => {
  // 现在：链接 https://b23.tv/ss41410，合集第 1 集 = 本地第 1 集，默认规则
  const sb = seasonBinding(1)
  const unchanged = {
    link: 'https://b23.tv/ss41410',
    candidate: { kind: 'bangumi', current: true },
    mapping: { from: 1, to: 1 },
    patterns: defaultPatterns,
  }
  it.each([
    ['没有改动', {}, {}],
    ['改集号对应', { mapping: { from: 14, to: 1 } }, { mappingFrom: 14, mappingTo: 1 }],
    [
      '调整集号规则的顺序也算改动',
      { patterns: defaultPatterns.toReversed() },
      { episodePatterns: defaultPatterns.toReversed() },
    ],
    [
      '选了别的候选',
      { candidate: { kind: 'ugcSeason', current: false } },
      { link: 'https://b23.tv/ss41410', kind: 'ugcSeason' },
    ],
    [
      '同一个合集换了一种链接',
      { link: 'https://www.bilibili.com/bangumi/play/ss41410' },
      { link: 'https://www.bilibili.com/bangumi/play/ss41410', kind: 'bangumi' },
    ],
  ])('%s', (_, edit, want) => {
    expect(seasonBindingPatch(sb, { ...unchanged, ...edit })).toEqual(want)
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
