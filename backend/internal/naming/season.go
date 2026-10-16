package naming

import "fmt"

// SeriesType 剧的类型，与 series.type 列的取值一致。
type SeriesType string

const (
	TypeTV    SeriesType = "tv"
	TypeMovie SeriesType = "movie"
)

// SeasonName 季对外的名称（弹弹play 的 animeTitle）：第 1 季和电影为"剧名"，第 N 季为"剧名 第N季"，
// 第 0 季为"剧名 特别篇"，都不带年份。Parse 认得"第N季""特别篇"这两种写法，能从它拆回剧名和季号，
// 拿它搜索能找回这一季。
func SeasonName(t SeriesType, seriesTitle string, number int) string {
	switch {
	case t == TypeMovie || number == 1:
		return seriesTitle
	case number == 0:
		return seriesTitle + " 特别篇"
	default:
		return fmt.Sprintf("%s 第%d季", seriesTitle, number)
	}
}

// SeasonKind 季的类别。
type SeasonKind int

const (
	KindSeries  SeasonKind = iota + 1 // 剧集的第 1 季及以后
	KindSpecial                       // 剧集的第 0 季
	KindMovie
)

// KindOf 季的类别：电影的季为电影，剧集的第 0 季为特别篇，其余为剧集。
func KindOf(t SeriesType, number int) SeasonKind {
	switch {
	case t == TypeMovie:
		return KindMovie
	case number == 0:
		return KindSpecial
	default:
		return KindSeries
	}
}
