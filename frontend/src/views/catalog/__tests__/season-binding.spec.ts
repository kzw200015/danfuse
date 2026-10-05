import { describe, expect, it } from 'vitest'

import type { Binding } from '@/api/bindings'
import type { CollectionCandidate, PreviewItem, SeasonBindingItem } from '@/api/season-bindings'
import type { Episode } from '@/api/series'
import {
  candidateText,
  itemStateText,
  parseMappingNumber,
  previewTarget,
  previewTargetText,
} from '../season-binding'

function item(number: number | null, unmatchedReason: string | null = null): PreviewItem {
  return { label: '条目', note: null, number, unmatchedReason }
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
  note: null,
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
    defaultMapping: { from: 1, to: 1 },
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
