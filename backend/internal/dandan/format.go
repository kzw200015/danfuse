package dandan

import (
	"fmt"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/provider"
)

// kinds 季的类别在协议里的 type（AnimeType 枚举）和 typeDescription 里的类型名。
var kinds = map[provider.SeasonKind]struct{ animeType, name string }{
	provider.KindSeries:  {"tvseries", "剧集"},
	provider.KindSpecial: {"tvspecial", "特别篇"},
	provider.KindMovie:   {"movie", "电影"},
}

// toAnime 一季转成协议里的作品。集按 Provider 给的顺序（集号升序）输出，不补占位：
// 插件按"集号 − 首集标题里的 N"作为下标取集，目录中间缺集时会错位。
func toAnime(s provider.Season) anime {
	kind := kinds[s.Kind]
	a := anime{
		AnimeID:         s.ID,
		AnimeTitle:      s.Name,
		Type:            kind.animeType,
		TypeDescription: kind.name,
		Episodes:        make([]episode, len(s.Episodes)),
	}
	if s.Year != nil {
		a.TypeDescription = fmt.Sprintf("%s · %d", kind.name, *s.Year) // 如"剧集 · 2011"
	}
	for i, e := range s.Episodes {
		a.Episodes[i] = episode{EpisodeID: e.ID, EpisodeTitle: episodeTitle(e)}
	}
	return a
}

// episodeTitle "第N话 {集标题}"，没有集标题时只写"第N话"。协议没有集号字段，
// 插件从首集标题的"第N话"解析起始集号。
func episodeTitle(e provider.Episode) string {
	if e.Title == "" {
		return fmt.Sprintf("第%d话", e.Number)
	}
	return fmt.Sprintf("第%d话 %s", e.Number, e.Title)
}

// userPrefixes p 里用户字段的平台前缀，插件靠它按来源过滤（[BiliBili] 归为 B 站）。前缀是协议对平台的叫法，
// 所以放在弹弹 API 一侧；没有平台的来源不加前缀，插件把它归为"弹弹"。
var userPrefixes = map[danmaku.Platform]string{
	danmaku.PlatformBilibili: "[BiliBili]",
}

// toComment 一条弹幕转成协议里的 comment：cid 由平台与原始 ID 现算；
// p 为"秒（两位小数）,模式,颜色（十进制）,用户"，用户字段只写平台前缀，后面不跟任何内容。
// 协议只定义了模式 1、4、5，逆向（6）输出为 1。
func toComment(it danmaku.Item) comment {
	mode := it.Mode
	if mode == danmaku.ModeReverse {
		mode = danmaku.ModeScroll
	}
	return comment{
		CID: danmaku.CID(it.Platform, it.SourceID),
		P:   fmt.Sprintf("%.2f,%d,%d,%s", float64(it.TimeMs)/1000, mode, it.Color, userPrefixes[it.Platform]),
		M:   it.Text,
	}
}
