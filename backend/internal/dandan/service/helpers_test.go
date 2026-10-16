package service_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/dandan/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/naming"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// searchCatalog 搜索测试用的目录。同步的顺序（剧 ID 的顺序）和年份都与期望的排序相反，
// 排序因此只能来自剧名长度、年份降序且无年份最后这几条规则；只有同名同年的电影和剧集靠剧 ID 分先后。
// 目录里的季都没有绑定。
func searchCatalog() []catalog.Item {
	starSea := func(year int, seasons ...catalog.Season) *catalog.Series {
		s := testenv.TV("星海旅人", new(year), seasons...)
		s.OriginalTitle = "ほしうみの旅人"
		return s
	}
	lighthouse := testenv.Movie("长夜灯塔", new(2020), 5400)
	lighthouse.OriginalTitle = "Night Lighthouse"
	return []catalog.Item{
		testenv.Item(testenv.TV("星海旅人外传", new(2021), testenv.Season(1, "", testenv.Episode(1, "", 1440)))),
		testenv.Item(testenv.TV("星海旅人", nil, testenv.Season(1, "", testenv.Episode(1, "", 1440)))),
		testenv.Item(testenv.Movie("星海旅人", new(2019), 6600)),
		testenv.Item(starSea(2019,
			testenv.Season(2, "归航篇", testenv.Episode(14, "", 1440), testenv.Episode(13, "启程", 1440)),
			testenv.Season(0, "Specials", testenv.Episode(1, "番外", 600)),
			testenv.Season(1, "", testenv.Episode(1, "", 1440), testenv.Episode(2, "", 1440)),
		)),
		testenv.Item(starSea(2023, testenv.Season(1, "", testenv.Episode(1, "", 1440)))),
		testenv.Item(lighthouse),
		testenv.Item(testenv.TV("やがて君になる", new(2018), testenv.Season(1, "", testenv.Episode(1, "", 1440)))),
		testenv.Item(testenv.TV("オーバーロード", new(2015), testenv.Season(1, "", testenv.Episode(1, "", 1440)), testenv.Season(2, "", testenv.Episode(1, "", 1440)))),
		testenv.Item(testenv.TV("Night Watch", new(2010), testenv.Season(1, "", testenv.Episode(1, "", 1440)))),
	}
}

// describe 把一季写成一行，便于整体比较："名称 · 类别 年份 · 共 N 集 · 集号 集标题, …"，N 是总集数。
func describe(s service.Season) string {
	kinds := map[naming.SeasonKind]string{naming.KindSeries: "剧集", naming.KindSpecial: "特别篇", naming.KindMovie: "电影"}
	year := "-"
	if s.Year != nil {
		year = fmt.Sprint(*s.Year)
	}
	episodes := make([]string, len(s.Episodes))
	for i, e := range s.Episodes {
		episodes[i] = strings.TrimSpace(fmt.Sprintf("%d %s", e.Number, e.Title))
	}
	return fmt.Sprintf("%s · %s %s · 共 %d 集 · %s", s.Name, kinds[s.Kind], year, s.EpisodeCount, strings.Join(episodes, ", "))
}

func TestMain(m *testing.M) { dbtest.Main(m) }
