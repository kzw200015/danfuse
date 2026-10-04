import { describe, expect, it } from 'vitest'

import type { Binding, BindingStatus } from '@/api/bindings'
import type { Episode, Season, SeriesDetail, SeriesSummary } from '@/api/series'
import {
  bindingStats,
  defaultSeason,
  durationMismatch,
  filterSeries,
  formatDuration,
  parseId,
  parseOffset,
  resolveSelection,
  seasonLabel,
  seasonName,
  type Selection,
} from '../catalog'

function summary(id: number, title: string, originalTitle: string | null, year: number | null) {
  return {
    id,
    type: 'tv',
    title,
    originalTitle,
    year,
    posterImageId: null,
    seasonCount: 1,
    episodeCount: 1,
    boundEpisodeCount: 0,
    bindingCount: 0,
    deadBindingCount: 0,
  } satisfies SeriesSummary
}

function season(id: number, number: number, episodeIds: number[] = []): Season {
  return {
    id,
    number,
    title: null,
    episodes: episodeIds.map((e, i) => ({
      id: e,
      number: i + 1,
      title: null,
      duration: null,
      bindings: [],
    })),
  }
}

describe('filterSeries', () => {
  const list = [
    summary(1, '星海旅人', 'Star Voyager', 2023),
    summary(2, '雾港谜案', null, 2021),
    summary(3, '星海旅人', 'Star Voyager', 2019),
    summary(4, '青石巷日常', null, null),
  ]
  const titles = (keyword: string) =>
    filterSeries(list, keyword).map((s) => `${s.title} ${s.year ?? '-'}`)

  it.each([
    ['', ['青石巷日常 -', '雾港谜案 2021', '星海旅人 2019', '星海旅人 2023']],
    ['  ', ['青石巷日常 -', '雾港谜案 2021', '星海旅人 2019', '星海旅人 2023']],
    ['星海', ['星海旅人 2019', '星海旅人 2023']],
    [' VOYAGER ', ['星海旅人 2019', '星海旅人 2023']],
    ['谜案', ['雾港谜案 2021']],
    ['不存在', []],
  ])('筛选 %j', (keyword, want) => {
    expect(titles(keyword)).toEqual(want)
  })

  it('标题里的数字按数值排序', () => {
    const numbered = [summary(1, '物语 10', null, null), summary(2, '物语 2', null, null)]
    expect(filterSeries(numbered, '').map((s) => s.title)).toEqual(['物语 2', '物语 10'])
  })

  it('不改动传入的列表', () => {
    filterSeries(list, '')
    expect(list.map((s) => s.id)).toEqual([1, 2, 3, 4])
  })
})

describe('defaultSeason', () => {
  it.each([
    ['有第 1 季时选第 1 季', [season(10, 0), season(11, 1), season(12, 2)], 11],
    ['没有第 1 季时选第一个季', [season(10, 0), season(12, 2)], 10],
    ['没有季', [], undefined],
  ])('%s', (_, seasons, want) => {
    expect(defaultSeason(seasons)?.id).toBe(want)
  })
})

/** 选中结果写成一行，便于比较 */
function selected(sel: Selection) {
  return sel.missing
    ? `missing ${sel.missing}`
    : `season ${sel.season?.id ?? '-'} episode ${sel.episode?.id ?? '-'}`
}

describe('resolveSelection', () => {
  const tv: SeriesDetail = {
    id: 1,
    type: 'tv',
    title: '星海旅人',
    originalTitle: null,
    year: 2019,
    posterImageId: null,
    seasons: [season(10, 0, [100]), season(11, 1, [110, 111])],
  }
  const movie: SeriesDetail = {
    ...tv,
    id: 2,
    type: 'movie',
    seasons: [season(20, 1, [200])],
  }
  it.each([
    ['剧：默认第 1 季，不选集', tv, undefined, undefined, 'season 11 episode -'],
    ['剧：选中季', tv, '10', undefined, 'season 10 episode -'],
    ['剧：选中集', tv, '11', '111', 'season 11 episode 111'],
    ['剧：季不在这部剧里', tv, '20', undefined, 'missing season'],
    ['剧：季 ID 不是数字', tv, 'abc', undefined, 'missing season'],
    ['剧：集不在这一季里', tv, '10', '110', 'missing episode'],
    ['剧：没有季', { ...tv, seasons: [] }, undefined, undefined, 'season - episode -'],
    ['电影：直接选中唯一那一集', movie, undefined, undefined, 'season 20 episode 200'],
    ['电影：给了季也选中那一集', movie, '20', undefined, 'season 20 episode 200'],
    ['电影：季不存在', movie, '10', undefined, 'missing season'],
  ])('%s', (_, series, seasonId, episodeId, want) => {
    expect(selected(resolveSelection(series, seasonId, episodeId))).toBe(want)
  })
})

describe('季的标签与名称', () => {
  it.each([
    [0, '特别篇', '第 0 季（特别篇）'],
    [1, 'S1', '第 1 季'],
    [12, 'S12', '第 12 季'],
  ])('第 %i 季', (number, label, name) => {
    expect(seasonLabel(season(1, number))).toBe(label)
    expect(seasonName(season(1, number))).toBe(name)
  })
})

describe('formatDuration', () => {
  it.each([
    [null, '—'],
    [0, '0:00'],
    [45, '0:45'],
    [1420, '23:40'],
    [3600, '1:00:00'],
    [5405, '1:30:05'],
  ])('%j 秒', (seconds, want) => {
    expect(formatDuration(seconds)).toBe(want)
  })
})

describe('bindingStats', () => {
  /** 一集，按给出的状态各带一个绑定 */
  function episode(...statuses: BindingStatus[]): Episode {
    return {
      ...season(1, 1, [1]).episodes[0]!,
      bindings: statuses.map((status, id) => ({ id, status }) as Binding),
    }
  }

  it.each([
    ['没有集', [], { bound: 0, dead: 0 }],
    ['都没有绑定', [episode(), episode()], { bound: 0, dead: 0 }],
    [
      '一集有多个绑定只算一个已绑定集，失效按绑定计',
      [episode('active', 'dead'), episode(), episode('dead')],
      { bound: 2, dead: 2 },
    ],
  ])('%s', (_, episodes, want) => {
    expect(bindingStats(episodes)).toEqual(want)
  })
})

describe('durationMismatch', () => {
  it.each([
    ['相同', 1420, 1420, null],
    ['相差不到 3 秒', 1422, 1420, null],
    ['长 3 秒', 1423, 1420, 3],
    ['短 7 秒', 1413, 1420, -7],
    ['本集没有时长', 1420, null, null],
  ])('%s', (_, source, episode, want) => {
    expect(durationMismatch(source, episode)).toBe(want)
  })
})

describe('parseOffset', () => {
  it.each([
    ['1.5', 1.5],
    ['1.234', 1.234],
    ['-0.5', -0.5],
    [' -2 ', -2],
    ['+3', 3],
    ['.5', 0.5],
    ['7.', 7],
    ['0', 0],
    ['86400', 86400],
    ['-86400', -86400],
  ])('%j 合法', (text, want) => {
    expect(parseOffset(text)).toBe(want)
  })

  it.each([
    '',
    '  ',
    'abc',
    '1,5',
    '1.2.3',
    '--1',
    '86400.1',
    '-86401',
    '1e3',
    'Infinity',
    'NaN',
    '0x10',
    // 小数最多到毫秒
    '0.0001',
    '.1234',
  ])('%j 不合法', (text) => {
    expect(parseOffset(text)).toBeNull()
  })
})

describe('parseId', () => {
  it.each([
    ['1', 1],
    ['42', 42],
    ['0', undefined],
    ['-1', undefined],
    ['1.5', undefined],
    ['01', undefined],
    ['1e1', undefined],
    ['abc', undefined],
    ['', undefined],
  ])('%j', (param, want) => {
    expect(parseId(param)).toBe(want)
  })
})
