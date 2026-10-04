import type { Episode, Season, SeriesDetail, SeriesSummary } from '@/api/series'

// 标题里的数字按数值比较："第2部"排在"第10部"前面
const collator = new Intl.Collator('zh', { numeric: true })

/** 按剧名或原名筛选（不区分大小写），按标题排序，同名的按年份排 */
export function filterSeries(list: SeriesSummary[], keyword: string): SeriesSummary[] {
  const k = keyword.trim().toLowerCase()
  return list
    .filter(
      (s) =>
        !k || s.title.toLowerCase().includes(k) || !!s.originalTitle?.toLowerCase().includes(k),
    )
    .toSorted((a, b) => collator.compare(a.title, b.title) || (a.year ?? 0) - (b.year ?? 0))
}

/** 默认选中的季：第 1 季，没有就第一个季 */
export function defaultSeason(seasons: Season[]): Season | undefined {
  return seasons.find((s) => s.number === 1) ?? seasons[0]
}

/** 地址栏选中的季和集；ID 不在这部剧里时 missing 指出是哪一级 */
export type Selection =
  | { missing: 'season'; season?: undefined; episode?: undefined }
  | { missing: 'episode'; season: Season; episode?: undefined }
  | { missing?: undefined; season?: Season; episode?: Episode }

/** 解析地址栏里的季 ID、集 ID。没给季时选默认的季；电影没给集时选中唯一那一集（电影没有季面板） */
export function resolveSelection(
  series: SeriesDetail,
  seasonId: string | undefined,
  episodeId: string | undefined,
): Selection {
  const season =
    seasonId === undefined
      ? defaultSeason(series.seasons)
      : series.seasons.find((s) => String(s.id) === seasonId)
  if (!season) {
    return seasonId === undefined ? {} : { missing: 'season' }
  }
  if (episodeId === undefined) {
    return { season, episode: series.type === 'movie' ? season.episodes[0] : undefined }
  }
  const episode = season.episodes.find((e) => String(e.id) === episodeId)
  return episode ? { season, episode } : { missing: 'episode', season }
}

/** 季切换上的标签 */
export function seasonLabel(season: Season) {
  return season.number === 0 ? '特别篇' : `S${season.number}`
}

export function seasonName(season: Season) {
  return season.number === 0 ? '第 0 季（特别篇）' : `第 ${season.number} 季`
}

/** 年份与类型，例如"2019 · 剧集" */
export function seriesMeta(series: Pick<SeriesSummary, 'type' | 'year'>) {
  return `${series.year ?? '年份未知'} · ${series.type === 'movie' ? '电影' : '剧集'}`
}

/** 时长（秒）显示为 m:ss 或 h:mm:ss，没有时显示"—" */
export function formatDuration(seconds: number | null) {
  if (seconds === null) return '—'
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const ss = String(seconds % 60).padStart(2, '0')
  return h ? `${h}:${String(m).padStart(2, '0')}:${ss}` : `${m}:${ss}`
}

/** 一组集的绑定统计：已绑定的集数（至少有一个绑定）、失效的绑定数 */
export function bindingStats(episodes: Episode[]) {
  return {
    bound: episodes.filter((e) => e.bindings.length > 0).length,
    dead: episodes.flatMap((e) => e.bindings).filter((b) => b.status === 'dead').length,
  }
}

/** 弹幕源比本集长多少秒，负数表示短；本集没有时长、或相差不到 3 秒时为 null，不必标出 */
export function durationMismatch(source: number, episode: number | null) {
  if (episode === null) return null
  const diff = source - episode
  return Math.abs(diff) >= 3 ? diff : null
}

/** 偏移绝对值的上限，秒（一天），与后端一致 */
export const MAX_OFFSET = 86400

/**
 * 偏移输入框里的秒数：十进制数，可带正负号，小数最多到毫秒（三位；读取时校正后的时间也只精确到毫秒），
 * 绝对值不超过 MAX_OFFSET。不合法时为 null（空的、指数写法、Infinity 这类都不算），在前端拦下，不发请求。
 */
export function parseOffset(text: string) {
  const s = text.trim()
  if (!/^[+-]?(\d+(\.\d{0,3})?|\.\d{1,3})$/.test(s)) return null
  const offset = Number(s)
  return Math.abs(offset) <= MAX_OFFSET ? offset : null
}

/** 目录页的地址：/catalog[/剧[/季[/集]]] */
export function catalogPath(...ids: number[]) {
  return ['/catalog', ...ids].join('/')
}

/** 地址栏里的剧 ID：规范写法的正整数（与季、集 ID 按字符串比较一致，"01""1e1"都不算），否则为 undefined */
export function parseId(param: string) {
  return /^[1-9]\d*$/.test(param) ? Number(param) : undefined
}
