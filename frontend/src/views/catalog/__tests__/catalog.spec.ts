import { describe, expect, it } from 'vitest'

import type { Season, SeriesDetail, SeriesSummary } from '@/api/series'
import {
  defaultSeason,
  filterSeries,
  formatDuration,
  parseId,
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
    seasonCount: 1,
    episodeCount: 1,
  } satisfies SeriesSummary
}

function season(id: number, number: number, episodeIds: number[] = []): Season {
  return {
    id,
    number,
    title: null,
    episodes: episodeIds.map((e, i) => ({ id: e, number: i + 1, title: null, duration: null })),
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
