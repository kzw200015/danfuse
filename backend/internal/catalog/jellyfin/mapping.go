package jellyfin

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
)

// maxMultiEpisodeSpan 多集文件的范围超过这个数视为解析异常，只写起始那一集并记警告。
const maxMultiEpisodeSpan = 10

const ticksPerSecond = 10_000_000

// mapMovie 电影合成为一部 type=movie 的剧，下面一个第 1 季、一个第 1 集：季、集标题都为空，集时长取电影的时长。
func mapMovie(movie item) catalog.Item {
	return catalog.Item{
		Name: displayName(movie),
		Series: &catalog.Series{
			Type:          catalog.TypeMovie,
			Title:         strings.TrimSpace(movie.Name),
			OriginalTitle: strings.TrimSpace(movie.OriginalTitle),
			Year:          movie.ProductionYear,
			Seasons: []catalog.Season{{
				Number:   1,
				Episodes: []catalog.Episode{{Number: 1, Duration: seconds(movie.RunTimeTicks)}},
			}},
		},
	}
}

// mapSeries 把一部剧和按剧递归查到的季、集条目翻译成 catalog.Item：跳过的项写进 Warnings，
// 没有任何有效的集时 Series 为 nil。children 归 mapSeries 所有，会被就地过滤、排序。
//   - 只用 SeriesId 等于这部剧的条目：媒体库开启自动合并剧时（默认开启），按剧查询会带出合并在一起的其他剧文件夹的季和集；
//   - 条目按 Id 排序后处理，同一季同一集号出现多次时（10.11 的多版本、重叠的多集文件）按这个顺序排列；
//   - 季号取集的 ParentIndexNumber，不取季文件夹的 IndexNumber（中文季文件夹解析不出季号）；
//   - 季标题取同一季号的 Season 条目的 Name，有多个时取 Id 最小的，没有就为空；IndexNumber 为空的 Season 条目不用。
func mapSeries(series item, children []item) catalog.Item {
	own := slices.DeleteFunc(children, func(c item) bool { return c.SeriesID != series.ID })
	slices.SortFunc(own, byID)

	result := catalog.Item{Name: displayName(series)}
	titles := map[int]string{}
	episodes := map[int][]catalog.Episode{}
	for _, c := range own {
		switch c.Type {
		case typeSeason:
			if c.IndexNumber == nil {
				continue
			}
			if _, ok := titles[*c.IndexNumber]; !ok {
				titles[*c.IndexNumber] = strings.TrimSpace(c.Name)
			}
		case typeEpisode:
			season, eps, warning := mapEpisode(c)
			if warning != "" {
				result.Warnings = append(result.Warnings, warning)
			}
			if len(eps) > 0 {
				episodes[season] = append(episodes[season], eps...)
			}
		}
	}
	if len(episodes) == 0 {
		result.Warnings = append(result.Warnings, "没有有效的集，整部跳过")
		return result
	}

	s := &catalog.Series{
		Type:          catalog.TypeTV,
		Title:         strings.TrimSpace(series.Name),
		OriginalTitle: strings.TrimSpace(series.OriginalTitle),
		Year:          series.ProductionYear,
	}
	for _, number := range slices.Sorted(maps.Keys(episodes)) {
		eps := episodes[number]
		// 稳定排序：同一集号的多个条目保持 Id 顺序
		slices.SortStableFunc(eps, func(a, b catalog.Episode) int { return cmp.Compare(a.Number, b.Number) })
		s.Seasons = append(s.Seasons, catalog.Season{Number: number, Title: titles[number], Episodes: eps})
	}
	result.Series = s
	return result
}

// mapEpisode 翻译一个集条目，返回季号和展开后的集；季号或集号为空、为负数时跳过并返回警告。
// 多集文件 IndexNumber..IndexNumberEnd 拆成范围内的每一集，除集号外完全相同，标题都取条目的 Name，时长都是整个文件的时长；
// 范围超过 maxMultiEpisodeSpan 或倒序时视为解析异常，只写起始那一集，同时返回警告。
func mapEpisode(e item) (season int, eps []catalog.Episode, warning string) {
	switch {
	case e.ParentIndexNumber == nil:
		return 0, nil, episodeLabel(e) + "没有季号，已跳过"
	case *e.ParentIndexNumber < 0:
		return 0, nil, episodeLabel(e) + "的季号为负数，已跳过"
	case e.IndexNumber == nil:
		return 0, nil, episodeLabel(e) + "没有集号，已跳过"
	case *e.IndexNumber < 0:
		return 0, nil, episodeLabel(e) + "的集号为负数，已跳过"
	}

	first := *e.IndexNumber
	last := first
	if end := e.IndexNumberEnd; end != nil && *end != first {
		if *end > first && *end-first < maxMultiEpisodeSpan {
			last = *end
		} else {
			warning = fmt.Sprintf("%s的集号范围 %d-%d 异常，只写入第 %d 集", episodeLabel(e), first, *end, first)
		}
	}

	ep := catalog.Episode{Title: strings.TrimSpace(e.Name), Duration: seconds(e.RunTimeTicks)}
	for n := first; n <= last; n++ {
		ep.Number = n
		eps = append(eps, ep)
	}
	return *e.ParentIndexNumber, eps, warning
}

// episodeLabel 警告里指代一个集条目：名称，加上所在季的名称和集号（有的话），方便用户在 Jellyfin 里定位。
// 例如 集「雾港谜案」（第1季，第 2 集）。
func episodeLabel(e item) string {
	var where []string
	if e.SeasonName != "" {
		where = append(where, e.SeasonName)
	}
	if e.IndexNumber != nil {
		where = append(where, fmt.Sprintf("第 %d 集", *e.IndexNumber))
	}
	if len(where) == 0 {
		return fmt.Sprintf("集「%s」", e.Name)
	}
	return fmt.Sprintf("集「%s」（%s）", e.Name, strings.Join(where, "，"))
}

// displayName 剧在 Jellyfin 里的名称，用于警告和错误信息；名称为空时用条目 Id 代替。
func displayName(it item) string {
	if name := strings.TrimSpace(it.Name); name != "" {
		return name
	}
	return "Jellyfin 条目 " + it.ID
}

// seconds 把 RunTimeTicks 换算成秒，四舍五入；缺失或不到 1 秒时为 nil。
func seconds(ticks *int64) *int {
	if ticks == nil {
		return nil
	}
	s := int((*ticks + ticksPerSecond/2) / ticksPerSecond)
	if s <= 0 {
		return nil
	}
	return &s
}
