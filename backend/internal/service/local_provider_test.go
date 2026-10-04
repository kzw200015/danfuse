package service

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/provider"
	"github.com/kzw200015/danfuse/backend/internal/repository"
)

// searchCatalog 搜索测试用的目录。同步的顺序（剧 ID 的顺序）和年份都与期望的排序相反，
// 排序因此只能来自剧名长度、年份降序且无年份最后这几条规则；只有同名同年的电影和剧集靠剧 ID 分先后。
// 目录里的季都没有绑定。
func searchCatalog() []catalog.Item {
	starSea := func(year int, seasons ...catalog.Season) *catalog.Series {
		s := tv("星海旅人", new(year), seasons...)
		s.OriginalTitle = "ほしうみの旅人"
		return s
	}
	lighthouse := movie("长夜灯塔", new(2020), 5400)
	lighthouse.OriginalTitle = "Night Lighthouse"
	return []catalog.Item{
		item(tv("星海旅人外传", new(2021), season(1, "", episode(1, "", 1440)))),
		item(tv("星海旅人", nil, season(1, "", episode(1, "", 1440)))),
		item(movie("星海旅人", new(2019), 6600)),
		item(starSea(2019,
			season(2, "归航篇", episode(14, "", 1440), episode(13, "启程", 1440)),
			season(0, "Specials", episode(1, "番外", 600)),
			season(1, "", episode(1, "", 1440), episode(2, "", 1440)),
		)),
		item(starSea(2023, season(1, "", episode(1, "", 1440)))),
		item(lighthouse),
		item(tv("やがて君になる", new(2018), season(1, "", episode(1, "", 1440)))),
		item(tv("オーバーロード", new(2015), season(1, "", episode(1, "", 1440)), season(2, "", episode(1, "", 1440)))),
		item(tv("Night Watch", new(2010), season(1, "", episode(1, "", 1440)))),
	}
}

// describe 把一季写成一行，便于整体比较："名称 · 类别 年份 · 季号 · 集号 集标题, …"。
func describe(s provider.Season) string {
	kinds := map[provider.SeasonKind]string{provider.KindSeries: "剧集", provider.KindSpecial: "特别篇", provider.KindMovie: "电影"}
	year, number := "-", "-"
	if s.Year != nil {
		year = fmt.Sprint(*s.Year)
	}
	if s.Number != nil {
		number = fmt.Sprint(*s.Number)
	}
	episodes := make([]string, len(s.Episodes))
	for i, e := range s.Episodes {
		episodes[i] = strings.TrimSpace(fmt.Sprintf("%d %s", e.Number, e.Title))
	}
	return fmt.Sprintf("%s · %s %s · %s · %s", s.Name, kinds[s.Kind], year, number, strings.Join(episodes, ", "))
}

func searchSeasons(t *testing.T, pool *pgxpool.Pool, keyword string, maxSeasons int) (seasons []string, hasMore bool) {
	t.Helper()
	result, err := NewLocalProvider(repository.NewStore(pool)).Search(t.Context(), provider.SearchQuery{Keyword: keyword, MaxSeasons: maxSeasons})
	if err != nil {
		t.Fatalf("Search(%q): %v", keyword, err)
	}
	for _, s := range result.Seasons {
		seasons = append(seasons, describe(s))
	}
	return seasons, result.HasMore
}

func TestLocalSearch(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		syncOnce(t, newTestService(t, pool, &fakeSource{items: searchCatalog()}))

		tests := []struct {
			name    string
			keyword string
			want    []string
		}{
			{
				// 剧名完全相同的在前（剧名短）；同名剧按年份降序，无年份最后，同年的按剧 ID；
				// 同一部剧按季号，特别篇最后
				"剧名", "星海旅人", []string{
					"星海旅人 · 剧集 2023 · 1 · 1",
					"星海旅人 · 电影 2019 · 1 · 1",
					"星海旅人 · 剧集 2019 · 1 · 1, 2",
					"星海旅人 第2季 · 剧集 2019 · 2 · 13 启程, 14",
					"星海旅人 特别篇 · 特别篇 2019 · 0 · 1 番外",
					"星海旅人 · 剧集 - · 1 · 1",
					"星海旅人外传 · 剧集 2021 · 1 · 1",
				},
			},
			{"中文标题中间的一段", "海旅", []string{
				"星海旅人 · 剧集 2023 · 1 · 1",
				"星海旅人 · 电影 2019 · 1 · 1",
				"星海旅人 · 剧集 2019 · 1 · 1, 2",
				"星海旅人 第2季 · 剧集 2019 · 2 · 13 启程, 14",
				"星海旅人 特别篇 · 特别篇 2019 · 0 · 1 番外",
				"星海旅人 · 剧集 - · 1 · 1",
				"星海旅人外传 · 剧集 2021 · 1 · 1",
			}},
			{"剧名紧跟季号命中那一季", "星海旅人2", []string{"星海旅人 第2季 · 剧集 2019 · 2 · 13 启程, 14"}},
			{"自己输出的季名称", "星海旅人 特别篇", []string{"星海旅人 特别篇 · 特别篇 2019 · 0 · 1 番外"}},
			{"季标题", "归航", []string{"星海旅人 第2季 · 剧集 2019 · 2 · 13 启程, 14"}},
			{"日文标题中的一段", "君にな", []string{"やがて君になる · 剧集 2018 · 1 · 1"}},
			{"带长音符的假名与季号", "オーバーロード2", []string{"オーバーロード 第2季 · 剧集 2015 · 2 · 1"}},
			{"日文原名中的一段", "ほしうみ", []string{
				"星海旅人 · 剧集 2023 · 1 · 1",
				"星海旅人 · 剧集 2019 · 1 · 1, 2",
				"星海旅人 第2季 · 剧集 2019 · 2 · 13 启程, 14",
				"星海旅人 特别篇 · 特别篇 2019 · 0 · 1 番外",
			}},
			{"英文原名，不区分大小写", "night LIGHTHOUSE", []string{"长夜灯塔 · 电影 2020 · 1 · 1"}},
			{"剧名命中排在原名命中之前，剧名再长也一样", "night", []string{
				"Night Watch · 剧集 2010 · 1 · 1",
				"长夜灯塔 · 电影 2020 · 1 · 1",
			}},
			{"英文整词匹配，不做前缀", "light", nil},
			{"所有词都要命中", "星海旅人 灯塔", nil},
			{"切不出词", "・！", nil},
		}
		for _, tt := range tests {
			got, hasMore := searchSeasons(t, pool, tt.keyword, 50)
			if !slices.Equal(got, tt.want) || hasMore {
				t.Errorf("%s：Search(%q) = %q, hasMore %v\nwant %q", tt.name, tt.keyword, got, hasMore, tt.want)
			}
		}
	})
}

func TestLocalSearchHasMore(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		seasons := make([]catalog.Season, 51)
		for i := range seasons {
			seasons[i] = season(i+1, "", episode(1, "", 1440))
		}
		syncOnce(t, newTestService(t, pool, &fakeSource{items: []catalog.Item{item(tv("长篇连载", nil, seasons...))}}))

		for _, tt := range []struct {
			maxSeasons  int
			wantLen     int
			wantHasMore bool
		}{
			{50, 50, true},
			{51, 51, false},
		} {
			got, hasMore := searchSeasons(t, pool, "长篇连载", tt.maxSeasons)
			if len(got) != tt.wantLen || hasMore != tt.wantHasMore {
				t.Errorf("最多 %d 季：返回 %d 季，hasMore %v；want %d 季，hasMore %v", tt.maxSeasons, len(got), hasMore, tt.wantLen, tt.wantHasMore)
			}
			if len(got) > 0 && got[0] != "长篇连载 · 剧集 - · 1 · 1" {
				t.Errorf("最多 %d 季：第一季 = %q, want 第 1 季", tt.maxSeasons, got[0])
			}
		}
	})
}
