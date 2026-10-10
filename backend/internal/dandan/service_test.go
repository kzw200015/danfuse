package dandan_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/dandan"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
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
func describe(s dandan.Season) string {
	kinds := map[catalog.SeasonKind]string{catalog.KindSeries: "剧集", catalog.KindSpecial: "特别篇", catalog.KindMovie: "电影"}
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

func searchSeasons(t *testing.T, pool *pgxpool.Pool, q dandan.SearchQuery) (seasons []string, hasMore bool) {
	t.Helper()
	result, err := testenv.New(pool, testenv.Logger(t)).Dandan.Search(t.Context(), q)
	if err != nil {
		t.Fatalf("Search(%+v): %v", q, err)
	}
	for _, s := range result.Seasons {
		seasons = append(seasons, describe(s))
	}
	return seasons, result.HasMore
}

func TestMain(m *testing.M) { dbtest.Main(m) }

func TestSearch(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		testenv.SyncOnce(t, testenv.StartSync(t, pool, &testenv.FakeCatalog{Items: searchCatalog()}))
		// 剧名完全相同的在前（剧名短）；同名剧按年份降序，无年份最后，同年的按剧 ID；同一部剧按季号，特别篇最后
		starSea := []string{
			"星海旅人 · 剧集 2023 · 共 1 集 · 1",
			"星海旅人 · 电影 2019 · 共 1 集 · 1",
			"星海旅人 · 剧集 2019 · 共 2 集 · 1, 2",
			"星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程, 14",
			"星海旅人 特别篇 · 特别篇 2019 · 共 1 集 · 1 番外",
			"星海旅人 · 剧集 - · 共 1 集 · 1",
			"星海旅人外传 · 剧集 2021 · 共 1 集 · 1",
		}

		tests := []struct {
			name    string
			keyword string
			want    []string
		}{
			{"剧名", "星海旅人", starSea},
			{"中文标题中间的一段", "海旅", starSea},
			{"剧名紧跟季号命中那一季", "星海旅人2", []string{"星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程, 14"}},
			{"自己输出的季名称", "星海旅人 特别篇", []string{"星海旅人 特别篇 · 特别篇 2019 · 共 1 集 · 1 番外"}},
			{"季标题", "归航", []string{"星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程, 14"}},
			{"日文标题中的一段", "君にな", []string{"やがて君になる · 剧集 2018 · 共 1 集 · 1"}},
			{"带长音符的假名与季号", "オーバーロード2", []string{"オーバーロード 第2季 · 剧集 2015 · 共 1 集 · 1"}},
			{"日文原名中的一段", "ほしうみ", []string{
				"星海旅人 · 剧集 2023 · 共 1 集 · 1",
				"星海旅人 · 剧集 2019 · 共 2 集 · 1, 2",
				"星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程, 14",
				"星海旅人 特别篇 · 特别篇 2019 · 共 1 集 · 1 番外",
			}},
			{"英文原名，不区分大小写", "night LIGHTHOUSE", []string{"长夜灯塔 · 电影 2020 · 共 1 集 · 1"}},
			{"剧名命中排在原名命中之前，剧名再长也一样", "night", []string{
				"Night Watch · 剧集 2010 · 共 1 集 · 1",
				"长夜灯塔 · 电影 2020 · 共 1 集 · 1",
			}},
			{"英文整词匹配，不做前缀", "light", nil},
			{"所有词都要命中", "星海旅人 灯塔", nil},
			{"切不出词", "・！", nil},
		}
		for _, tt := range tests {
			got, hasMore := searchSeasons(t, pool, dandan.SearchQuery{Keyword: tt.keyword, MaxSeasons: 50})
			if !slices.Equal(got, tt.want) || hasMore {
				t.Errorf("%s：Search(%q) = %q, hasMore %v\nwant %q", tt.name, tt.keyword, got, hasMore, tt.want)
			}
		}
	})
}

func TestSearchHasMore(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		seasons := make([]catalog.Season, 51)
		for i := range seasons {
			seasons[i] = testenv.Season(i+1, "", testenv.Episode(1, "", 1440))
		}
		testenv.SyncOnce(t, testenv.StartSync(t, pool, &testenv.FakeCatalog{Items: []catalog.Item{testenv.Item(testenv.TV("长篇连载", nil, seasons...))}}))

		for _, tt := range []struct {
			maxSeasons  int
			wantLen     int
			wantHasMore bool
		}{
			{50, 50, true},
			{51, 51, false},
		} {
			got, hasMore := searchSeasons(t, pool, dandan.SearchQuery{Keyword: "长篇连载", MaxSeasons: tt.maxSeasons})
			if len(got) != tt.wantLen || hasMore != tt.wantHasMore {
				t.Errorf("最多 %d 季：返回 %d 季，hasMore %v；want %d 季，hasMore %v", tt.maxSeasons, len(got), hasMore, tt.wantLen, tt.wantHasMore)
			}
			if len(got) > 0 && got[0] != "长篇连载 · 剧集 - · 共 1 集 · 1" {
				t.Errorf("最多 %d 季：第一季 = %q, want 第 1 季", tt.maxSeasons, got[0])
			}
		}
	})
}

// TestSearchSeasonEpisode 关键词里写明的季号、集号（catalog.ParseName）按季号、集号精确过滤，集号以参数优先；
// 按集号过滤时只返回有这一集的季，每季只带这一集，过滤在截断之前。季带着所属剧的标题，有原名时加上原名。
func TestSearchSeasonEpisode(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		testenv.SyncOnce(t, testenv.StartSync(t, pool, &testenv.FakeCatalog{Items: searchCatalog()}))
		episodeOne := []string{
			"星海旅人 · 剧集 2023 · 共 1 集 · 1",
			"星海旅人 · 电影 2019 · 共 1 集 · 1",
			"星海旅人 · 剧集 2019 · 共 2 集 · 1",
			"星海旅人 特别篇 · 特别篇 2019 · 共 1 集 · 1 番外",
			"星海旅人 · 剧集 - · 共 1 集 · 1",
			"星海旅人外传 · 剧集 2021 · 共 1 集 · 1",
		}
		secondSeason := "星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程, 14"
		episode13 := []string{"星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程"}
		tests := []struct {
			name    string
			q       dandan.SearchQuery
			want    []string
			hasMore bool
		}{
			{"集号参数", dandan.SearchQuery{Keyword: "星海旅人", Episode: new(1)}, episodeOne, false},
			{"截断之前过滤", dandan.SearchQuery{Keyword: "星海旅人", Episode: new(13), MaxSeasons: 1}, episode13, false},
			{"截断之后还有", dandan.SearchQuery{Keyword: "星海旅人", Episode: new(1), MaxSeasons: 2}, episodeOne[:2], true},
			{"没有这一集", dandan.SearchQuery{Keyword: "星海旅人", Episode: new(99)}, nil, false},
			{"关键词里的集号", dandan.SearchQuery{Keyword: "星海旅人 第13话"}, episode13, false},
			{"集号参数优先", dandan.SearchQuery{Keyword: "星海旅人 第13话", Episode: new(1)}, episodeOne, false},
			{"关键词里的季号", dandan.SearchQuery{Keyword: "星海旅人 第2季"}, []string{secondSeason}, false},
			{"关键词里的季号和集号", dandan.SearchQuery{Keyword: "星海旅人 S02E13"}, episode13, false},
			{"这一季没有这一集", dandan.SearchQuery{Keyword: "星海旅人 第2季 第1话"}, nil, false},
			// 电影唯一的一季也是第 1 季
			{"只写季号", dandan.SearchQuery{Keyword: "星海旅人 S01"}, []string{
				"星海旅人 · 剧集 2023 · 共 1 集 · 1",
				"星海旅人 · 电影 2019 · 共 1 集 · 1",
				"星海旅人 · 剧集 2019 · 共 2 集 · 1, 2",
				"星海旅人 · 剧集 - · 共 1 集 · 1",
				"星海旅人外传 · 剧集 2021 · 共 1 集 · 1",
			}, false},
			{"没有标题", dandan.SearchQuery{Keyword: "第2季 第13话"}, nil, false},
		}
		for _, tt := range tests {
			if tt.q.MaxSeasons == 0 {
				tt.q.MaxSeasons = 50
			}
			got, hasMore := searchSeasons(t, pool, tt.q)
			if !slices.Equal(got, tt.want) || hasMore != tt.hasMore {
				t.Errorf("%s：Search(%+v) = %q, hasMore %v\nwant %q, hasMore %v", tt.name, tt.q, got, hasMore, tt.want, tt.hasMore)
			}
		}

		// 按集号过滤时总集数不变
		svc := testenv.New(pool, testenv.Logger(t)).Dandan
		result, err := svc.Search(t.Context(), dandan.SearchQuery{Keyword: "星海旅人 S02E13", MaxSeasons: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Seasons) != 1 || len(result.Seasons[0].Episodes) != 1 || result.Seasons[0].EpisodeCount != 2 {
			t.Errorf("Search(星海旅人 S02E13) = %+v, want 一季，带着一集，总集数 2", result.Seasons)
		}

		for keyword, want := range map[string][]string{
			"长夜灯塔":   {"长夜灯塔", "Night Lighthouse"},
			"星海旅人外传": {"星海旅人外传"},
		} {
			result, err := svc.Search(t.Context(), dandan.SearchQuery{Keyword: keyword, MaxSeasons: 50})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Seasons) != 1 || !slices.Equal(result.Seasons[0].Titles, want) {
				t.Errorf("Search(%q) 的标题 = %+v, want 一季，标题 %q", keyword, result.Seasons, want)
			}
		}
	})
}

// TestSeason 按 ID 取到的季与搜索结果里的同一季相同（剧集、电影、特别篇、没有年份）；季不存在时 found 为 false。
func TestSeason(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		testenv.SyncOnce(t, testenv.StartSync(t, pool, &testenv.FakeCatalog{Items: searchCatalog()}))
		svc := testenv.New(pool, testenv.Logger(t)).Dandan

		result, err := svc.Search(t.Context(), dandan.SearchQuery{Keyword: "星海旅人", MaxSeasons: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Seasons) != 7 {
			t.Fatalf("搜到 %d 季，want 7", len(result.Seasons))
		}
		for _, want := range result.Seasons {
			got, found, err := svc.Season(t.Context(), want.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !found || describe(got) != describe(want) || got.ID != want.ID {
				t.Errorf("Season(%d) = %q, found %v; want %q", want.ID, describe(got), found, describe(want))
			}
		}

		if got, found, err := svc.Season(t.Context(), 999999); err != nil || found {
			t.Errorf("Season(999999) = %q, found %v, err %v; want 不存在", describe(got), found, err)
		}
	})
}

// TestSyncRecomputesSearchVectors 同步重算一部剧所有季的搜索列，按搜索的结果检查。
func TestSyncRecomputesSearchVectors(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCatalog{}
		svc := testenv.StartSync(t, pool, src)
		starSea := testenv.TV("星海旅人", new(2019), testenv.Season(1, "", testenv.Episode(1, "", 1440)), testenv.Season(2, "归航篇", testenv.Episode(1, "", 1440)))
		starSea.OriginalTitle = "Star Voyager"
		src.Items = []catalog.Item{testenv.Item(starSea)}
		testenv.SyncOnce(t, svc)

		// 原名变了，这次目录源只给出第 1 季：第 2 季的搜索列也要按新原名重算
		starSea = testenv.TV("星海旅人", new(2019), testenv.Season(1, "", testenv.Episode(1, "", 1440)))
		starSea.OriginalTitle = "Star Traveler"
		src.Items = []catalog.Item{testenv.Item(starSea)}
		testenv.SyncOnce(t, svc)

		bothSeasons := []string{"星海旅人 · 剧集 2019 · 共 1 集 · 1", "星海旅人 第2季 · 剧集 2019 · 共 1 集 · 1"}
		for keyword, want := range map[string][]string{
			"Star Traveler": bothSeasons,
			"Star Voyager":  nil,
			"归航":            {"星海旅人 第2季 · 剧集 2019 · 共 1 集 · 1"}, // 这次没有给出的季保留原来的季标题
		} {
			if got, _ := searchSeasons(t, pool, dandan.SearchQuery{Keyword: keyword, MaxSeasons: 50}); !slices.Equal(got, want) {
				t.Errorf("Search(%q) = %q, want %q", keyword, got, want)
			}
		}
	})
}
