import type { AppendFilesResult } from '@/api/bindings'
import type { Episode, Season, SeriesDetail, SeriesSummary, SeriesType } from '@/api/series'

// 标题里的数字按数值比较："第2部"排在"第10部"前面
const collator = new Intl.Collator('zh', { numeric: true })

/** 剧列表的分类：全部，或只看一种类型。在地址栏的查询参数 ?type= 里，没有时为全部 */
export type SeriesCategory = 'all' | SeriesType

export const seriesCategories: { value: SeriesCategory; label: string }[] = [
  { value: 'all', label: '全部' },
  { value: 'tv', label: '剧集' },
  { value: 'movie', label: '电影' },
]

/** 地址栏里的分类；没有或不认识的值都当作全部 */
export function parseCategory(search: URLSearchParams): SeriesCategory {
  const type = search.get('type')
  return type === 'tv' || type === 'movie' ? type : 'all'
}

/** 选中某个分类后的查询串：其他查询参数不动，全部时去掉 type */
export function categorySearch(search: URLSearchParams, category: SeriesCategory) {
  const next = new URLSearchParams(search)
  if (category === 'all') next.delete('type')
  else next.set('type', category)
  const s = next.toString()
  return s ? `?${s}` : ''
}

/**
 * 按分类、剧名或原名筛选（不区分大小写），followingOnly 时只留追更中的剧（有开着追更的季绑定），
 * noTmdbIdOnly 时只留没有 TMDB ID 的剧；条件同时生效。
 * 按年份倒序，年份未知的排在最后，同一年的按标题排
 */
export function filterSeries(
  list: SeriesSummary[],
  {
    keyword = '',
    category = 'all',
    followingOnly = false,
    noTmdbIdOnly = false,
  }: {
    keyword?: string
    category?: SeriesCategory
    followingOnly?: boolean
    noTmdbIdOnly?: boolean
  } = {},
): SeriesSummary[] {
  const k = keyword.trim().toLowerCase()
  return list
    .filter(
      (s) =>
        (category === 'all' || s.type === category) &&
        (!followingOnly || s.following) &&
        (!noTmdbIdOnly || s.tmdbId === null) &&
        (!k || s.title.toLowerCase().includes(k) || !!s.originalTitle?.toLowerCase().includes(k)),
    )
    .toSorted(
      (a, b) => (b.year ?? -Infinity) - (a.year ?? -Infinity) || collator.compare(a.title, b.title),
    )
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

/** 剧在 TMDB 上的页面；电视剧和电影各有一套编号，按类型分路径。没有 TMDB ID 时为 null */
export function tmdbUrl(series: Pick<SeriesDetail, 'type' | 'tmdbId'>) {
  if (series.tmdbId === null) return null
  return `https://www.themoviedb.org/${series.type === 'movie' ? 'movie' : 'tv'}/${series.tmdbId}`
}

/** 一组集的绑定统计：已绑定的集数（至少有一个绑定）、失效的绑定数 */
export function bindingStats(episodes: Episode[]) {
  return {
    bound: episodes.filter((e) => e.bindings.length > 0).length,
    dead: episodes.flatMap((e) => e.bindings).filter((b) => b.status === 'dead').length,
  }
}

/**
 * 删除剧、季或集的确认框里的一句：随之删除的下级有多少，由已加载的剧详情算出。
 * 剧写季数和集数（电影在界面上没有季和集，不写），季写集数，都写绑定数和弹幕条数。
 */
export function deletionImpact(node: SeriesDetail | Season | Episode) {
  const parts: string[] = []
  let episodes: Episode[]
  if ('seasons' in node) {
    episodes = node.seasons.flatMap((s) => s.episodes)
    if (node.type === 'tv') parts.push(`${node.seasons.length} 季`, `${episodes.length} 集`)
  } else if ('episodes' in node) {
    episodes = node.episodes
    parts.push(`${episodes.length} 集`)
  } else {
    episodes = [node]
  }
  const bindings = episodes.flatMap((e) => e.bindings)
  const danmaku = bindings.reduce((n, b) => n + b.danmakuCount, 0)
  parts.push(
    bindings.length > 0
      ? `${bindings.length} 个绑定（共 ${danmaku.toLocaleString()} 条弹幕）`
      : '0 个绑定',
  )
  return `将一起删除 ${parts.join('、')}，无法恢复。`
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

/** 弹幕时间（int32 毫秒）的上限 */
const MAX_TIME_MS = 2 ** 31 - 1

/**
 * 跳转输入的时间：秒、m:ss 或 h:mm:ss（冒号后的分、秒为 0～59，可省去前导 0），返回毫秒；不合法或超出范围时为 null。
 */
export function parseTimestamp(text: string) {
  const s = text.trim()
  if (!/^\d+(:[0-5]?\d){0,2}$/.test(s)) return null
  const seconds = s.split(':').reduce((acc, part) => acc * 60 + Number(part), 0)
  const ms = seconds * 1000
  return ms <= MAX_TIME_MS ? ms : null
}

/** 目录页的地址：/catalog[/剧[/季[/集]]] */
export function catalogPath(...ids: number[]) {
  return ['/catalog', ...ids].join('/')
}

/** 选择弹幕文件时的文件类型：目前只支持 B 站的 XML 弹幕文件 */
export const DANMAKU_FILE_ACCEPT = '.xml,text/xml,application/xml'

/** 文件大小，例如 512 B、12.3 KB、1.2 MB */
export function formatFileSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

/** 追加文件的结果提示：新加入几份、新增几条，已在绑定里的跳过几份 */
export function appendFilesMessage({
  files,
  skipped,
  added,
}: Pick<AppendFilesResult, 'files' | 'skipped' | 'added'>) {
  if (files === 0) return `没有新文件：${skipped} 份都已在这个绑定里`
  const message = `已加入 ${files} 份文件，新增 ${added.toLocaleString()} 条弹幕`
  return skipped > 0 ? `${message}；${skipped} 份已在绑定里，跳过` : message
}
