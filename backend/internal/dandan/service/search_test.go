package service_test

import (
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/dandan/service"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

func searchSeasons(t *testing.T, pool *pgxpool.Pool, q service.SearchQuery) (seasons []string, hasMore bool) {
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
			got, hasMore := searchSeasons(t, pool, service.SearchQuery{Keyword: tt.keyword, MaxSeasons: 50})
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
			got, hasMore := searchSeasons(t, pool, service.SearchQuery{Keyword: "长篇连载", MaxSeasons: tt.maxSeasons})
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
			q       service.SearchQuery
			want    []string
			hasMore bool
		}{
			{"集号参数", service.SearchQuery{Keyword: "星海旅人", Episode: new(1)}, episodeOne, false},
			{"截断之前过滤", service.SearchQuery{Keyword: "星海旅人", Episode: new(13), MaxSeasons: 1}, episode13, false},
			{"截断之后还有", service.SearchQuery{Keyword: "星海旅人", Episode: new(1), MaxSeasons: 2}, episodeOne[:2], true},
			{"没有这一集", service.SearchQuery{Keyword: "星海旅人", Episode: new(99)}, nil, false},
			{"关键词里的集号", service.SearchQuery{Keyword: "星海旅人 第13话"}, episode13, false},
			{"集号参数优先", service.SearchQuery{Keyword: "星海旅人 第13话", Episode: new(1)}, episodeOne, false},
			{"关键词里的季号", service.SearchQuery{Keyword: "星海旅人 第2季"}, []string{secondSeason}, false},
			{"关键词里的季号和集号", service.SearchQuery{Keyword: "星海旅人 S02E13"}, episode13, false},
			{"这一季没有这一集", service.SearchQuery{Keyword: "星海旅人 第2季 第1话"}, nil, false},
			// 电影唯一的一季也是第 1 季
			{"只写季号", service.SearchQuery{Keyword: "星海旅人 S01"}, []string{
				"星海旅人 · 剧集 2023 · 共 1 集 · 1",
				"星海旅人 · 电影 2019 · 共 1 集 · 1",
				"星海旅人 · 剧集 2019 · 共 2 集 · 1, 2",
				"星海旅人 · 剧集 - · 共 1 集 · 1",
				"星海旅人外传 · 剧集 2021 · 共 1 集 · 1",
			}, false},
			{"没有标题", service.SearchQuery{Keyword: "第2季 第13话"}, nil, false},
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
		result, err := svc.Search(t.Context(), service.SearchQuery{Keyword: "星海旅人 S02E13", MaxSeasons: 50})
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
			result, err := svc.Search(t.Context(), service.SearchQuery{Keyword: keyword, MaxSeasons: 50})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Seasons) != 1 || !slices.Equal(result.Seasons[0].Titles, want) {
				t.Errorf("Search(%q) 的标题 = %+v, want 一季，标题 %q", keyword, result.Seasons, want)
			}
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
			if got, _ := searchSeasons(t, pool, service.SearchQuery{Keyword: keyword, MaxSeasons: 50}); !slices.Equal(got, want) {
				t.Errorf("Search(%q) = %q, want %q", keyword, got, want)
			}
		}
	})
}
