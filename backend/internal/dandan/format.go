package dandan

import (
	"fmt"

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
