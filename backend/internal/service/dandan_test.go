package service

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
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

// describe 把一季写成一行，便于整体比较："名称 · 类别 年份 · 共 N 集 · 集号 集标题, …"，N 是总集数。
func describe(s DandanSeason) string {
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

func searchSeasons(t *testing.T, pool *pgxpool.Pool, q DandanSearchQuery) (seasons []string, hasMore bool) {
	t.Helper()
	result, err := NewDandanService(repository.NewStore(pool), source.NewRegistry()).Search(t.Context(), q)
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
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		syncOnce(t, newTestService(t, pool, &fakeCatalog{items: searchCatalog()}))

		tests := []struct {
			name    string
			keyword string
			want    []string
		}{
			{
				// 剧名完全相同的在前（剧名短）；同名剧按年份降序，无年份最后，同年的按剧 ID；
				// 同一部剧按季号，特别篇最后
				"剧名", "星海旅人", []string{
					"星海旅人 · 剧集 2023 · 共 1 集 · 1",
					"星海旅人 · 电影 2019 · 共 1 集 · 1",
					"星海旅人 · 剧集 2019 · 共 2 集 · 1, 2",
					"星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程, 14",
					"星海旅人 特别篇 · 特别篇 2019 · 共 1 集 · 1 番外",
					"星海旅人 · 剧集 - · 共 1 集 · 1",
					"星海旅人外传 · 剧集 2021 · 共 1 集 · 1",
				},
			},
			{"中文标题中间的一段", "海旅", []string{
				"星海旅人 · 剧集 2023 · 共 1 集 · 1",
				"星海旅人 · 电影 2019 · 共 1 集 · 1",
				"星海旅人 · 剧集 2019 · 共 2 集 · 1, 2",
				"星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程, 14",
				"星海旅人 特别篇 · 特别篇 2019 · 共 1 集 · 1 番外",
				"星海旅人 · 剧集 - · 共 1 集 · 1",
				"星海旅人外传 · 剧集 2021 · 共 1 集 · 1",
			}},
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
			got, hasMore := searchSeasons(t, pool, DandanSearchQuery{Keyword: tt.keyword, MaxSeasons: 50})
			if !slices.Equal(got, tt.want) || hasMore {
				t.Errorf("%s：Search(%q) = %q, hasMore %v\nwant %q", tt.name, tt.keyword, got, hasMore, tt.want)
			}
		}
	})
}

func TestSearchHasMore(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		seasons := make([]catalog.Season, 51)
		for i := range seasons {
			seasons[i] = season(i+1, "", episode(1, "", 1440))
		}
		syncOnce(t, newTestService(t, pool, &fakeCatalog{items: []catalog.Item{item(tv("长篇连载", nil, seasons...))}}))

		for _, tt := range []struct {
			maxSeasons  int
			wantLen     int
			wantHasMore bool
		}{
			{50, 50, true},
			{51, 51, false},
		} {
			got, hasMore := searchSeasons(t, pool, DandanSearchQuery{Keyword: "长篇连载", MaxSeasons: tt.maxSeasons})
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
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		syncOnce(t, newTestService(t, pool, &fakeCatalog{items: searchCatalog()}))
		episodeOne := []string{
			"星海旅人 · 剧集 2023 · 共 1 集 · 1",
			"星海旅人 · 电影 2019 · 共 1 集 · 1",
			"星海旅人 · 剧集 2019 · 共 2 集 · 1",
			"星海旅人 特别篇 · 特别篇 2019 · 共 1 集 · 1 番外",
			"星海旅人 · 剧集 - · 共 1 集 · 1",
			"星海旅人外传 · 剧集 2021 · 共 1 集 · 1",
		}
		secondSeason := "星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程, 14"
		tests := []struct {
			name    string
			q       DandanSearchQuery
			want    []string
			hasMore bool
		}{
			{"集号参数", DandanSearchQuery{Keyword: "星海旅人", Episode: new(1)}, episodeOne, false},
			{"截断之前过滤", DandanSearchQuery{Keyword: "星海旅人", Episode: new(13), MaxSeasons: 1}, []string{"星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程"}, false},
			{"截断之后还有", DandanSearchQuery{Keyword: "星海旅人", Episode: new(1), MaxSeasons: 2}, episodeOne[:2], true},
			{"没有这一集", DandanSearchQuery{Keyword: "星海旅人", Episode: new(99)}, nil, false},
			{"关键词里的集号", DandanSearchQuery{Keyword: "星海旅人 第13话"}, []string{"星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程"}, false},
			{"集号参数优先", DandanSearchQuery{Keyword: "星海旅人 第13话", Episode: new(1)}, episodeOne, false},
			{"关键词里的季号", DandanSearchQuery{Keyword: "星海旅人 第2季"}, []string{secondSeason}, false},
			{"关键词里的季号和集号", DandanSearchQuery{Keyword: "星海旅人 S02E13"}, []string{"星海旅人 第2季 · 剧集 2019 · 共 2 集 · 13 启程"}, false},
			{"这一季没有这一集", DandanSearchQuery{Keyword: "星海旅人 第2季 第1话"}, nil, false},
			// 电影唯一的一季也是第 1 季
			{"只写季号", DandanSearchQuery{Keyword: "星海旅人 S01"}, []string{
				"星海旅人 · 剧集 2023 · 共 1 集 · 1",
				"星海旅人 · 电影 2019 · 共 1 集 · 1",
				"星海旅人 · 剧集 2019 · 共 2 集 · 1, 2",
				"星海旅人 · 剧集 - · 共 1 集 · 1",
				"星海旅人外传 · 剧集 2021 · 共 1 集 · 1",
			}, false},
			{"没有标题", DandanSearchQuery{Keyword: "第2季 第13话"}, nil, false},
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
		svc := NewDandanService(repository.NewStore(pool), source.NewRegistry())
		result, err := svc.Search(t.Context(), DandanSearchQuery{Keyword: "星海旅人 S02E13", MaxSeasons: 50})
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
			result, err := svc.Search(t.Context(), DandanSearchQuery{Keyword: keyword, MaxSeasons: 50})
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
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		syncOnce(t, newTestService(t, pool, &fakeCatalog{items: searchCatalog()}))
		svc := NewDandanService(repository.NewStore(pool), source.NewRegistry())

		result, err := svc.Search(t.Context(), DandanSearchQuery{Keyword: "星海旅人", MaxSeasons: 50})
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

// platformAdapter 只回答 ID 和平台的假适配器：读取弹幕只用到这两项，调用其他方法会 panic。
type platformAdapter struct {
	source.Adapter
	id       string
	platform danmaku.Platform
}

func (a platformAdapter) ID() string                 { return a.id }
func (a platformAdapter) Platform() danmaku.Platform { return a.platform }

// newCommentsService 新库里写入两集（seedEpisodes）和 sql 里的绑定与弹幕，构造 DandanService。
// 注册的适配器：bilibili 在 B 站平台，fake 没有平台。
func newCommentsService(t *testing.T, sql string) *DandanService {
	t.Helper()
	pool := dbtest.Pool(t)
	seedEpisodes(t, pool)
	if _, err := pool.Exec(t.Context(), sql); err != nil {
		t.Fatal(err)
	}
	sources := source.NewRegistry(
		platformAdapter{id: "bilibili", platform: danmaku.PlatformBilibili},
		platformAdapter{id: "fake", platform: danmaku.PlatformNone},
	)
	return NewDandanService(repository.NewStore(pool), sources)
}

func TestComments(t *testing.T) {
	t.Parallel()
	svc := newCommentsService(t, `
		INSERT INTO bindings (episode_id, adapter, ref, title, duration, "offset", scale, status, danmaku_count) VALUES
			(1, 'bilibili', '{"aid": 1}', 'B 站投稿', 1420, 0, 1, 'active', 3),               -- 绑定 1
			(1, 'bilibili', '{"epId": 2}', 'B 站番剧', 1422, 10, 1, 'dead', 3),               -- 绑定 2：失效
			(1, 'fake', '{"name": "local"}', '没有平台的弹幕源', 2840, -2, 0.5, 'active', 2); -- 绑定 3
		INSERT INTO bindings (episode_id, kind, title, danmaku_count) VALUES (1, 'file', '弹幕文件', 1); -- 绑定 4
		INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text) VALUES
			(1, 101, 1000, 1, 16777215, '前排'),
			(1, 102, 61000, 6, 15138834, '逆向'),
			(1, 103, 120000, 4, 0, '底部'),
			(2, 101, 1000, 1, 16777215, '前排'), -- 原始 ID 与绑定 1 的那条相同；校正后相差 10 秒，只靠按 ID 去掉
			(2, 201, 51500, 1, 0, '逆向'),       -- 校正后 61.5 秒，与绑定 1 的那条相差 0.5 秒，按文本去掉
			(2, 202, 300000, 5, 255, '失效绑定的弹幕'),
			(3, 1, 2000, 1, 0, '太早了'),        -- 2 × 0.5 − 2 = −1 秒
			(3, 2, 10000, 1, 0, '没有平台'),
			(4, 103, 200000, 1, 0, '弹幕文件里的');  -- 原始 ID 与绑定 1 的那条相同，但弹幕文件不属于任何平台，不按 ID 去掉`)

	const bili = danmaku.PlatformBilibili
	tests := []struct {
		name      string
		episodeID int64
		want      []danmaku.Item
	}{
		{
			"多个绑定按 offset 与 scale 校正、跨源去重后按时间合并；失效绑定的弹幕照常输出，弹幕文件的弹幕不属于任何平台", 1, []danmaku.Item{
				{TimeMs: 1000, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "前排", SourceID: 101, Platform: bili},
				{TimeMs: 3000, Mode: danmaku.ModeScroll, Text: "没有平台", SourceID: 2, Platform: danmaku.PlatformNone},
				{TimeMs: 61000, Mode: danmaku.ModeReverse, Color: 0xE70012, Text: "逆向", SourceID: 102, Platform: bili},
				{TimeMs: 120000, Mode: danmaku.ModeBottom, Text: "底部", SourceID: 103, Platform: bili},
				{TimeMs: 200000, Mode: danmaku.ModeScroll, Text: "弹幕文件里的", SourceID: 103, Platform: danmaku.PlatformNone},
				{TimeMs: 310000, Mode: danmaku.ModeTop, Color: 0xFF, Text: "失效绑定的弹幕", SourceID: 202, Platform: bili},
			},
		},
		{"没有绑定", 2, nil},
		{"集不存在", 99, nil},
	}
	for _, tt := range tests {
		got, err := svc.Comments(t.Context(), tt.episodeID)
		if err != nil {
			t.Fatalf("%s：Comments(%d): %v", tt.name, tt.episodeID, err)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s：Comments(%d) = %+v\nwant %+v", tt.name, tt.episodeID, got, tt.want)
		}
	}
}

// TestCommentsUnknownAdapter 绑定的适配器没有注册：不知道弹幕在哪个平台，跨源去重和 cid 都无从谈起，按服务器内部错误返回。
func TestCommentsUnknownAdapter(t *testing.T) {
	t.Parallel()
	svc := newCommentsService(t, `
		INSERT INTO bindings (episode_id, adapter, ref, title, duration) VALUES
			(1, 'bilibili', '{"aid": 1}', 'B 站投稿', 1420),
			(1, 'gone', '{"id": 1}', '没有注册的适配器', 1420);`)

	if got, err := svc.Comments(t.Context(), 1); err == nil || !strings.Contains(err.Error(), `"gone"`) {
		t.Errorf("Comments() = %+v, %v；want 指出没有注册的适配器 gone 的错误", got, err)
	}
}
