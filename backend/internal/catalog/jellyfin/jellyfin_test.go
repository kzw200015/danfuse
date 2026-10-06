package jellyfin

import (
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
)

// e2e 环境里媒体库与剧的 Jellyfin Id，两个版本相同。
const (
	libShows  = "28e774baf8f2fd279e7d58da9890a7d2" // 番剧
	libMovies = "3227ce1e069754c594af25ea66d69fc7" // 电影

	seriesStarSea2019 = "a2e82866ba1950c858d34adbd471dda5" // 星海旅人 (2019)
	seriesFogHarbor   = "c311548d3a5ebeef512b28ff37a80d62" // 雾港谜案 (2021)
	seriesStarSea2023 = "e4892d8fc0b94beff829b05e504b6929" // 星海旅人 (2023)
	seriesStoneLane   = "fa5a9a8128b338dfb62899e69beb66dd" // 青石巷日常 (2022)
	movieLighthouse   = "2028c556be6ed0b7cb33571dfdc33b24" // 长夜灯塔 (2020)
)

func ep(number int, title string, duration int) catalog.Episode {
	return catalog.Episode{Number: number, Title: title, Duration: &duration}
}

// collect 取完迭代器，遇到错误时测试失败。
func collect(t *testing.T, items iter.Seq2[catalog.Item, error]) []catalog.Item {
	t.Helper()
	var got []catalog.Item
	for item, err := range items {
		if err != nil {
			t.Fatalf("Items 产出错误：%v", err)
		}
		got = append(got, item)
	}
	return got
}

func assertItems(t *testing.T, got, want []catalog.Item) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	g, _ := json.MarshalIndent(summarize(got), "", "  ")
	w, _ := json.MarshalIndent(summarize(want), "", "  ")
	t.Errorf("Items 不符\n got: %s\nwant: %s", g, w)
}

// summarize 失败输出用的摘要：海报换成 content-type、字节数和 sha256 前 8 位，PosterErr 换成错误文本，
// 免得几百 KB 的 base64 淹没差异、错误被编码成 {}。外层同名的字段在 JSON 里盖过内嵌结构体的字段。
func summarize(items []catalog.Item) any {
	type series struct {
		*catalog.Series
		Poster    string
		PosterErr string
	}
	type item struct {
		catalog.Item
		Series *series
	}
	result := make([]item, len(items))
	for i, it := range items {
		result[i] = item{Item: it}
		if it.Series == nil {
			continue
		}
		s := &series{Series: it.Series}
		if p := it.Series.Poster; p != nil {
			sum := sha256.Sum256(p.Data)
			s.Poster = fmt.Sprintf("%s，%d 字节，sha256 %x", p.ContentType, len(p.Data), sum[:4])
		}
		if err := it.Series.PosterErr; err != nil {
			s.PosterErr = err.Error()
		}
		result[i].Series = s
	}
	return result
}

// wantSamples 测试媒体库按规则应落成的目录（工单 19 的期望目录表）。两个版本的差别：
//   - 10.11 的剧名保留文件夹名里的年份，12.1 去掉（电影名两个版本都保留）；
//   - 同一集的两个版本：10.11 是 Id 不同的两集，按 Id 顺序排列；12.1 合并成一个；
//   - 文件名带季号、没有对应季文件夹时 Jellyfin 补建的季：10.11 叫"第 2 季"，12.1 叫"Season 2"。
//
// 12.1 里两部"星海旅人"被按剧名合并，按剧查询会互相带出对方的季和集，只保留 SeriesId 匹配的才能得到下面的结果。
// 海报：文件夹自带海报的三部（星海旅人 2019 的 JPEG、雾港谜案的 PNG、长夜灯塔的 JPEG）保持原格式，内容是抓取的样本。
func wantSamples(t *testing.T, version string) []catalog.Item {
	t.Helper()
	v1011 := version == "10.11"
	poster := func(itemID, contentType string) *catalog.Image {
		data, err := os.ReadFile(samplePath(filepath.Join("testdata", version), "image-"+itemID))
		if err != nil {
			t.Fatal(err)
		}
		return &catalog.Image{ContentType: contentType, Data: data}
	}
	name := func(title string, year int) string {
		if v1011 {
			return fmt.Sprintf("%s (%d)", title, year)
		}
		return title
	}
	tv := func(title string, year int, seasons ...catalog.Season) *catalog.Series {
		return &catalog.Series{Type: catalog.TypeTV, Title: name(title, year), Year: &year, Seasons: seasons}
	}

	starSeaS1 := []catalog.Episode{
		ep(1, "星海旅人 S01E01-E02", 50), ep(2, "星海旅人 S01E01-E02", 50), // 多集文件拆成两集
		ep(3, "星海旅人 S01E03", 30),
		ep(4, "星海旅人 S01E04 -", 34), // 1080p
	}
	if v1011 {
		starSeaS1 = append(starSeaS1, ep(4, "星海旅人 S01E04 -", 33)) // 720p，Id 更大，后写
	}
	fogHarborS2 := "Season 2"
	if v1011 {
		fogHarborS2 = "第 2 季"
	}

	starSea2019 := tv("星海旅人", 2019,
		catalog.Season{Number: 0, Title: "Specials", Episodes: []catalog.Episode{ep(1, "星海旅人 S00E01", 20)}},
		catalog.Season{Number: 1, Title: "第 1 季", Episodes: starSeaS1},
		catalog.Season{Number: 2, Title: "第 2 季", Episodes: []catalog.Episode{ep(1, "星海旅人 S02E01", 30), ep(2, "星海旅人 S02E02", 30)}},
	)
	starSea2019.Poster = poster(seriesStarSea2019, "image/jpeg")
	fogHarbor := tv("雾港谜案", 2021,
		catalog.Season{Number: 2, Title: fogHarborS2, Episodes: []catalog.Episode{ep(1, "雾港谜案 S02E01", 30), ep(2, "雾港谜案 S02E02", 30)}},
	)
	fogHarbor.Poster = poster(seriesFogHarbor, "image/png")

	return []catalog.Item{
		{
			Name: "长夜灯塔 (2020)",
			Series: &catalog.Series{
				Type: catalog.TypeMovie, Title: "长夜灯塔 (2020)", Year: new(2020),
				Poster:  poster(movieLighthouse, "image/jpeg"),
				Seasons: []catalog.Season{{Number: 1, Episodes: []catalog.Episode{ep(1, "", 45)}}},
			},
		},
		{Name: name("星海旅人", 2019), Series: starSea2019},
		{
			Name:   name("雾港谜案", 2021),
			Series: fogHarbor,
			// `第1季` 文件夹解析不出季号，里面文件名不带季号的集没有 ParentIndexNumber
			Warnings: []string{
				"集「雾港谜案」（第1季，第 2 集）没有季号，已跳过",
				"集「雾港谜案」（第1季，第 1 集）没有季号，已跳过",
			},
		},
		{
			Name: name("星海旅人", 2023),
			Series: tv("星海旅人", 2023,
				catalog.Season{Number: 1, Title: "第 1 季", Episodes: []catalog.Episode{ep(1, "星海旅人 S01E01", 30), ep(2, "星海旅人 S01E02", 30)}},
			),
		},
		{
			Name: name("青石巷日常", 2022),
			Series: tv("青石巷日常", 2022,
				catalog.Season{Number: 1, Title: "第 1 季", Episodes: []catalog.Episode{ep(1, "青石巷日常 S01E01", 30), ep(2, "青石巷日常 S01E02", 30)}},
			),
		},
	}
}

func TestListSamples(t *testing.T) {
	for _, v := range versions {
		t.Run(v.name, func(t *testing.T) {
			var fake *fakeJellyfin
			if *update {
				fake = recordFake(t, v.name)
			} else {
				fake = replayFake(t, v.name)
			}

			listing, err := fake.source("番剧", "电影", "其他", "不存在", "番剧").List(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			if listing.Total != 5 {
				t.Errorf("Total = %d, want 5", listing.Total)
			}
			wantWarnings := []string{"找不到媒体库「不存在」，已跳过", "媒体库「其他」的类型不是剧集或电影，已跳过"}
			if !slices.Equal(listing.Warnings, wantWarnings) {
				t.Errorf("Warnings = %q, want %q", listing.Warnings, wantWarnings)
			}
			got := collect(t, listing.Items) // 录制时取完才有海报的样本文件
			assertItems(t, got, wantSamples(t, v.name))
		})
	}
}

func TestItemsRequestsOnDemand(t *testing.T) {
	fake := replayFake(t, "12.1")
	listing, err := fake.source("番剧", "电影").List(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	// List 只列清单：媒体库按名称排序（电影在番剧之前）
	listed := []string{"virtual-folders.json", "items-" + libMovies + ".json", "items-" + libShows + ".json"}
	if got := fake.requested(); !slices.Equal(got, listed) {
		t.Fatalf("List 之后的请求 = %q, want %q", got, listed)
	}

	// 每要一项才取这一部剧的季和集、下载海报；电影不用取季和集，没有 Primary 图的不下载海报
	next, stop := iter.Pull2(listing.Items)
	defer stop()
	want := listed
	for _, requests := range [][]string{
		{"image-" + movieLighthouse},
		{"items-" + seriesStarSea2019 + ".json", "image-" + seriesStarSea2019},
		{"items-" + seriesFogHarbor + ".json", "image-" + seriesFogHarbor},
		{"items-" + seriesStarSea2023 + ".json"},
		{"items-" + seriesStoneLane + ".json"},
	} {
		if _, err, ok := next(); !ok || err != nil {
			t.Fatalf("next() = (%v, %v)", err, ok)
		}
		want = append(want, requests...)
		if got := fake.requested(); !slices.Equal(got, want) {
			t.Fatalf("请求 = %q, want %q", got, want)
		}
	}
	if _, _, ok := next(); ok {
		t.Error("每部剧恰好产出一项，取完后应结束")
	}
}

func TestItemsStopsOnBreak(t *testing.T) {
	fake := replayFake(t, "12.1")
	listing, err := fake.source("番剧").List(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	for range listing.Items {
		break
	}

	want := []string{
		"virtual-folders.json", "items-" + libShows + ".json",
		"items-" + seriesStarSea2019 + ".json", "image-" + seriesStarSea2019,
	}
	if got := fake.requested(); !slices.Equal(got, want) {
		t.Errorf("break 之后不应再请求，请求 = %q, want %q", got, want)
	}
}

func TestItemsEndsAfterError(t *testing.T) {
	fake := replayFake(t, "12.1")
	fake.failRequest("items-" + seriesFogHarbor + ".json")
	listing, err := fake.source("番剧").List(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for item, err := range listing.Items {
		switch {
		case err != nil:
			if !reflect.DeepEqual(item, catalog.Item{}) {
				t.Errorf("出错时应产出零值 Item，实际 %+v", item)
			}
			got = append(got, "error: "+err.Error())
		default:
			got = append(got, item.Name)
		}
	}

	if len(got) != 2 || got[0] != "星海旅人" || !strings.HasPrefix(got[1], "error: 取「雾港谜案」的季和集失败") {
		t.Errorf("产出 = %q, want 第一部剧、然后是错误", got)
	}
	if n := len(fake.requested()); n != 5 {
		t.Errorf("出错后不应再请求，共请求 %d 次，want 5", n)
	}
}

func TestListFails(t *testing.T) {
	folders := `[
		{"Name":"番剧","ItemId":"lib1","CollectionType":"tvshows"},
		{"Name":"混合","ItemId":"lib2"},
		{"Name":"音乐","ItemId":"lib3","CollectionType":"music"}
	]`
	tests := []struct {
		name      string
		libraries []string
		samples   map[string]string
		failOn    string
		wantErr   string // *catalog.Error 的提示
	}{
		{
			name:      "配置的媒体库都不可用",
			libraries: []string{"混合", "音乐", "动画"},
			samples:   map[string]string{"virtual-folders.json": folders},
			wantErr:   "配置的媒体库都不可用：找不到媒体库「动画」；媒体库「混合」的类型不是剧集或电影；媒体库「音乐」的类型不是剧集或电影",
		},
		{
			name:      "列出媒体库失败",
			libraries: []string{"番剧"},
			failOn:    "virtual-folders.json",
			wantErr:   "列出媒体库失败：Jellyfin 返回 HTTP 500",
		},
		{
			name:      "列出剧失败",
			libraries: []string{"番剧"},
			samples:   map[string]string{"virtual-folders.json": folders},
			failOn:    "items-lib1.json",
			wantErr:   "列出媒体库「番剧」的剧和电影失败：Jellyfin 返回 HTTP 500",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake(t, tt.samples)
			fake.failRequest(tt.failOn)

			_, err := fake.source(tt.libraries...).List(t.Context())
			if got := message(t, err); got != tt.wantErr {
				t.Errorf("List() 的提示 = %q, want %q", got, tt.wantErr)
			}
		})
	}
}

// TestListUnreachable 连不上 Jellyfin：提示"无法连接 Jellyfin"；底层原因与状态码错误同样格式，只有路径和系统错误，不带 URL 与查询串。
func TestListUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()

	_, err := New(config.Jellyfin{URL: srv.URL, APIKey: testAPIKey, Libraries: []string{"番剧"}, ListTimeout: time.Minute}).List(t.Context())

	if got, want := message(t, err), "列出媒体库失败：无法连接 Jellyfin"; got != want {
		t.Errorf("List() 的提示 = %q, want %q", got, want)
	}
	if cause := errors.Unwrap(err); cause == nil || !strings.HasPrefix(cause.Error(), "GET /Library/VirtualFolders: ") ||
		strings.Contains(cause.Error(), srv.URL) {
		t.Errorf("底层原因 = %v, want 「GET /Library/VirtualFolders: <原因>」，不带 URL", cause)
	}
}

// TestListRejected API Key 不对：提示检查 API Key，带上状态码。
func TestListRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	_, err := New(config.Jellyfin{URL: srv.URL, APIKey: "wrong", Libraries: []string{"番剧"}, ListTimeout: time.Minute}).List(t.Context())

	if got, want := message(t, err), "列出媒体库失败：Jellyfin 拒绝了请求，请检查 API Key（HTTP 401）"; got != want {
		t.Errorf("List() 的提示 = %q, want %q", got, want)
	}
}

// message 取出 *catalog.Error 的提示；err 不是 *catalog.Error 时测试失败。
func message(t *testing.T, err error) string {
	t.Helper()
	catalogErr, ok := errors.AsType[*catalog.Error](err)
	if !ok {
		t.Fatalf("err = %v, want *catalog.Error", err)
	}
	return catalogErr.Message
}

// TestMapping 用构造的响应覆盖 e2e 环境里没有的情形。媒体库"番剧"里只有一部剧 s1，一个用例一个行为。
func TestMapping(t *testing.T) {
	const folders = `[{"Name":"番剧","ItemId":"lib","CollectionType":"tvshows"}]`
	const defaultSeries = `{"Id":"s1","Type":"Series","Name":" 测试剧 ","OriginalTitle":" テスト ","ProductionYear":2020}`
	tv := func(seasons ...catalog.Season) *catalog.Series {
		return &catalog.Series{Type: catalog.TypeTV, Title: "测试剧", OriginalTitle: "テスト", Year: new(2020), Seasons: seasons}
	}
	episode := func(id, extra string) string {
		return fmt.Sprintf(`{"Id":%q,"Type":"Episode","SeriesId":"s1","Name":"集%s","RunTimeTicks":300000000,%s}`, id, id, extra)
	}
	season := func(id, extra string) string {
		return fmt.Sprintf(`{"Id":%q,"Type":"Season","SeriesId":"s1",%s}`, id, extra)
	}
	// valid 一个有效的集，让被跳过的集不至于让整部剧被跳过
	valid := episode("e9", `"ParentIndexNumber":1,"IndexNumber":9`)
	validSeason := catalog.Season{Number: 1, Episodes: []catalog.Episode{ep(9, "集e9", 30)}}

	tests := []struct {
		name     string
		series   string // 媒体库里的剧，为空时用 defaultSeries
		children []string
		want     catalog.Item
	}{
		{
			name:     "剧名为空白：用条目 Id 指代，标题为空（由核心的校验拒绝）",
			series:   `{"Id":"s1","Type":"Series","Name":"  "}`,
			children: []string{valid},
			want: catalog.Item{Name: "Jellyfin 条目 s1", Series: &catalog.Series{
				Type: catalog.TypeTV, Seasons: []catalog.Season{validSeason},
			}},
		},
		{
			name:     "多集文件范围恰好 10 集：全部展开",
			children: []string{episode("e1", `"ParentIndexNumber":1,"IndexNumber":1,"IndexNumberEnd":10`)},
			want: catalog.Item{Name: "测试剧", Series: tv(catalog.Season{Number: 1, Episodes: func() []catalog.Episode {
				var eps []catalog.Episode
				for n := 1; n <= 10; n++ {
					eps = append(eps, ep(n, "集e1", 30))
				}
				return eps
			}()})},
		},
		{
			name:     "多集文件范围超过 10 集：只写起始一集",
			children: []string{episode("e1", `"ParentIndexNumber":1,"IndexNumber":1,"IndexNumberEnd":11`)},
			want: catalog.Item{
				Name:     "测试剧",
				Series:   tv(catalog.Season{Number: 1, Episodes: []catalog.Episode{ep(1, "集e1", 30)}}),
				Warnings: []string{"集「集e1」（第 1 集）的集号范围 1-11 异常，只写入第 1 集"},
			},
		},
		{
			name:     "多集文件范围倒序：只写起始一集",
			children: []string{episode("e1", `"ParentIndexNumber":1,"IndexNumber":5,"IndexNumberEnd":3,"SeasonName":"第 1 季"`)},
			want: catalog.Item{
				Name:     "测试剧",
				Series:   tv(catalog.Season{Number: 1, Episodes: []catalog.Episode{ep(5, "集e1", 30)}}),
				Warnings: []string{"集「集e1」（第 1 季，第 5 集）的集号范围 5-3 异常，只写入第 5 集"},
			},
		},
		{
			name:     "没有季号的集跳过",
			children: []string{episode("e1", `"IndexNumber":1,"SeasonName":"第1季"`), valid},
			want: catalog.Item{
				Name: "测试剧", Series: tv(validSeason),
				Warnings: []string{"集「集e1」（第1季，第 1 集）没有季号，已跳过"},
			},
		},
		{
			name:     "季号为负数的集跳过",
			children: []string{episode("e1", `"ParentIndexNumber":-1,"IndexNumber":1`), valid},
			want: catalog.Item{
				Name: "测试剧", Series: tv(validSeason),
				Warnings: []string{"集「集e1」（第 1 集）的季号为负数，已跳过"},
			},
		},
		{
			name:     "没有集号的集跳过",
			children: []string{episode("e1", `"ParentIndexNumber":1,"SeasonName":"第 1 季"`), valid},
			want: catalog.Item{
				Name: "测试剧", Series: tv(validSeason),
				Warnings: []string{"集「集e1」（第 1 季）没有集号，已跳过"},
			},
		},
		{
			name:     "集号为负数的集跳过",
			children: []string{episode("e1", `"ParentIndexNumber":1,"IndexNumber":-2`), valid},
			want: catalog.Item{
				Name: "测试剧", Series: tv(validSeason),
				Warnings: []string{"集「集e1」（第 -2 集）的集号为负数，已跳过"},
			},
		},
		{
			name:     "第 0 季、第 0 集照常保留",
			children: []string{episode("e1", `"ParentIndexNumber":0,"IndexNumber":0`)},
			want:     catalog.Item{Name: "测试剧", Series: tv(catalog.Season{Number: 0, Episodes: []catalog.Episode{ep(0, "集e1", 30)}})},
		},
		{
			name: "没有有效的集：整部跳过",
			children: []string{
				season("a", `"Name":"第 1 季","IndexNumber":1`),
				episode("e1", `"IndexNumber":1`),
			},
			want: catalog.Item{Name: "测试剧", Warnings: []string{"集「集e1」（第 1 集）没有季号，已跳过", "没有有效的集，整部跳过"}},
		},
		{
			name: "季标题取同一季号 Id 最小的 Season，季号为空的 Season 不用",
			children: []string{
				season("c", `"Name":"第 1 季（后建）","IndexNumber":1`),
				season("b", `"Name":" 第一季 ","IndexNumber":1`),
				season("a", `"Name":"Season 01"`),
				season("d", `"Name":"第 3 季","IndexNumber":3`),
				episode("e1", `"ParentIndexNumber":1,"IndexNumber":1`),
				episode("e2", `"ParentIndexNumber":2,"IndexNumber":1`),
			},
			want: catalog.Item{Name: "测试剧", Series: tv(
				catalog.Season{Number: 1, Title: "第一季", Episodes: []catalog.Episode{ep(1, "集e1", 30)}},
				catalog.Season{Number: 2, Episodes: []catalog.Episode{ep(1, "集e2", 30)}},
			)},
		},
		{
			name: "同一季同一集号出现多次：按 Id 顺序排列",
			children: []string{
				episode("e3", `"ParentIndexNumber":1,"IndexNumber":2`),
				episode("e1", `"ParentIndexNumber":1,"IndexNumber":1,"IndexNumberEnd":2`),
				episode("e2", `"ParentIndexNumber":1,"IndexNumber":1`),
			},
			want: catalog.Item{Name: "测试剧", Series: tv(catalog.Season{Number: 1, Episodes: []catalog.Episode{
				ep(1, "集e1", 30), ep(1, "集e2", 30), ep(2, "集e1", 30), ep(2, "集e3", 30),
			}})},
		},
		{
			name: "其他剧的季和集被丢弃",
			children: []string{
				`{"Id":"x1","Type":"Season","SeriesId":"s2","Name":"别的剧的季","IndexNumber":1}`,
				`{"Id":"x2","Type":"Episode","SeriesId":"s2","Name":"别的剧的集","ParentIndexNumber":1,"IndexNumber":1}`,
				episode("e1", `"ParentIndexNumber":1,"IndexNumber":2`),
			},
			want: catalog.Item{Name: "测试剧", Series: tv(catalog.Season{Number: 1, Episodes: []catalog.Episode{ep(2, "集e1", 30)}})},
		},
		{
			name: "时长四舍五入到秒，缺失或不到 1 秒时为空",
			children: []string{
				`{"Id":"e1","Type":"Episode","SeriesId":"s1","Name":"a","ParentIndexNumber":1,"IndexNumber":1,"RunTimeTicks":14999999}`,
				`{"Id":"e2","Type":"Episode","SeriesId":"s1","Name":"b","ParentIndexNumber":1,"IndexNumber":2,"RunTimeTicks":15000000}`,
				`{"Id":"e3","Type":"Episode","SeriesId":"s1","Name":"c","ParentIndexNumber":1,"IndexNumber":3}`,
				`{"Id":"e4","Type":"Episode","SeriesId":"s1","Name":"d","ParentIndexNumber":1,"IndexNumber":4,"RunTimeTicks":0}`,
				`{"Id":"e5","Type":"Episode","SeriesId":"s1","Name":"e","ParentIndexNumber":1,"IndexNumber":5,"RunTimeTicks":4000000}`,
			},
			want: catalog.Item{Name: "测试剧", Series: tv(catalog.Season{Number: 1, Episodes: []catalog.Episode{
				ep(1, "a", 1), ep(2, "b", 2), {Number: 3, Title: "c"}, {Number: 4, Title: "d"}, {Number: 5, Title: "e"},
			}})},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			series := cmp.Or(tt.series, defaultSeries)
			fake := newFake(t, map[string]string{
				"virtual-folders.json": folders,
				"items-lib.json":       `{"Items":[` + series + `]}`,
				"items-s1.json":        `{"Items":[` + strings.Join(tt.children, ",") + `]}`,
			})
			listing, err := fake.source("番剧").List(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			assertItems(t, collect(t, listing.Items), []catalog.Item{tt.want})
		})
	}
}

// TestURLWithSubpath Jellyfin 挂在反向代理的子路径下：请求都发到子路径下面。
func TestURLWithSubpath(t *testing.T) {
	fake := newFake(t, map[string]string{
		"virtual-folders.json": `[{"Name":"电影","ItemId":"lib","CollectionType":"movies"}]`,
		"items-lib.json":       `{"Items":[{"Id":"m1","Type":"Movie","Name":"长夜灯塔"}]}`,
	})
	proxy := httptest.NewServer(http.StripPrefix("/jellyfin", fake)) // 子路径以外的请求返回 404
	t.Cleanup(proxy.Close)

	src := New(config.Jellyfin{URL: proxy.URL + "/jellyfin", APIKey: testAPIKey, Libraries: []string{"电影"}, ListTimeout: time.Minute})
	listing, err := src.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := collect(t, listing.Items); len(got) != 1 || got[0].Name != "长夜灯塔" {
		t.Errorf("Items = %+v, want 一部电影", got)
	}
}

func TestMapMovie(t *testing.T) {
	fake := newFake(t, map[string]string{
		"virtual-folders.json": `[{"Name":"电影","ItemId":"lib","CollectionType":"movies"}]`,
		"items-lib.json": `{"Items":[
			{"Id":"m2","Type":"Movie","Name":"无时长","OriginalTitle":"No Runtime"},
			{"Id":"m1","Type":"Movie","Name":"有时长","ProductionYear":1999,"RunTimeTicks":71234567890}
		]}`,
	})
	listing, err := fake.source("电影").List(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	movie := func(title, original string, year *int, duration *int) catalog.Item {
		return catalog.Item{Name: title, Series: &catalog.Series{
			Type: catalog.TypeMovie, Title: title, OriginalTitle: original, Year: year,
			Seasons: []catalog.Season{{Number: 1, Episodes: []catalog.Episode{{Number: 1, Duration: duration}}}},
		}}
	}
	assertItems(t, collect(t, listing.Items), []catalog.Item{
		movie("有时长", "", new(1999), new(7123)),
		movie("无时长", "No Runtime", nil, nil),
	})
	if n := len(fake.requested()); n != 2 {
		t.Errorf("电影不用取季和集，共请求 %d 次，want 2", n)
	}
}

// TestPoster 海报的三种状态。媒体库"番剧"里有两部剧：s1 按用例构造，s2 总有海报，用来确认 s1 的海报出问题时只影响它自己。
func TestPoster(t *testing.T) {
	const folders = `[{"Name":"番剧","ItemId":"lib","CollectionType":"tvshows"}]`
	const png = "\x89PNG\r\n\x1a\n海报" // 假的 PNG：不指定 Content-Type 时假 Jellyfin 按内容识别为 image/png
	const validChildren = `{"Items":[{"Id":"%[1]s-e1","Type":"Episode","SeriesId":%[1]q,"Name":"第1集","ParentIndexNumber":1,"IndexNumber":1}]}`
	pngImage := &catalog.Image{ContentType: "image/png", Data: []byte(png)}

	tests := []struct {
		name        string
		imageTags   string // s1 的 ImageTags
		children    string // s1 的季和集，为空时是一个有效的集
		image       string // s1 海报请求的响应体，为空时不应请求
		contentType string // s1 海报响应的 Content-Type
		failImage   bool   // s1 的海报请求返回 500
		wantSkipped bool   // s1 整部跳过
		wantPoster  *catalog.Image
		wantErr     string // PosterErr 的提示
	}{
		{
			name:        "有图：Content-Type 取响应头，不按内容识别",
			imageTags:   `{"Primary":"tag"}`,
			image:       png,
			contentType: "image/jpeg",
			wantPoster:  &catalog.Image{ContentType: "image/jpeg", Data: []byte(png)},
		},
		{
			name:      "没有 Primary 图：不下载，Poster 与 PosterErr 都为 nil",
			imageTags: `{"Thumb":"tag"}`,
		},
		{
			name:      "下载失败：状态码不是 200",
			imageTags: `{"Primary":"tag"}`,
			failImage: true,
			wantErr:   "Jellyfin 返回 HTTP 500",
		},
		{
			name:        "下载失败：响应不是图片",
			imageTags:   `{"Primary":"tag"}`,
			image:       "<html><body>请登录</body></html>",
			contentType: "text/html; charset=utf-8",
			wantErr:     "响应不是图片（Content-Type: text/html; charset=utf-8）",
		},
		{
			name:        "下载失败：不接受 SVG",
			imageTags:   `{"Primary":"tag"}`,
			image:       `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
			contentType: "image/svg+xml",
			wantErr:     "不接受 SVG 格式的海报",
		},
		{
			name:        "整部跳过的剧不下载海报",
			imageTags:   `{"Primary":"tag"}`,
			children:    `{"Items":[{"Id":"s1-e1","Type":"Episode","SeriesId":"s1","Name":"第1集","IndexNumber":1}]}`,
			wantSkipped: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			samples := map[string]string{
				"virtual-folders.json": folders,
				"items-lib.json": `{"Items":[
					{"Id":"s1","Type":"Series","Name":"甲","ImageTags":` + tt.imageTags + `},
					{"Id":"s2","Type":"Series","Name":"乙","ImageTags":{"Primary":"tag"}}
				]}`,
				"items-s1.json": cmp.Or(tt.children, fmt.Sprintf(validChildren, "s1")),
				"items-s2.json": fmt.Sprintf(validChildren, "s2"),
				"image-s2":      png,
			}
			fake := startFake(t, testAPIKey, func(name string, _ *http.Request) ([]byte, string) {
				if name == "image-s1" && tt.image != "" {
					return []byte(tt.image), tt.contentType
				}
				if body, ok := samples[name]; ok {
					return []byte(body), ""
				}
				return nil, ""
			})
			if tt.failImage {
				fake.failRequest("image-s1")
			}

			listing, err := fake.source("番剧").List(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			items := collect(t, listing.Items)

			if len(items) != 2 {
				t.Fatalf("产出 %d 项, want 2", len(items))
			}
			switch s1 := items[0].Series; {
			case tt.wantSkipped:
				if s1 != nil {
					t.Errorf("s1 应整部跳过，实际 %+v", s1)
				}
			case s1 == nil:
				t.Fatalf("s1 被跳过：%q", items[0].Warnings)
			default:
				if !reflect.DeepEqual(s1.Poster, tt.wantPoster) {
					t.Errorf("Poster = %+v, want %+v", s1.Poster, tt.wantPoster)
				}
				var gotErr string
				if s1.PosterErr != nil {
					gotErr = message(t, s1.PosterErr)
				}
				if gotErr != tt.wantErr {
					t.Errorf("PosterErr = %q, want %q", gotErr, tt.wantErr)
				}
				if len(items[0].Warnings) != 0 {
					t.Errorf("海报的状态不放进 Warnings，实际 %q", items[0].Warnings)
				}
			}
			if s2 := items[1].Series; s2 == nil || !reflect.DeepEqual(s2.Poster, pngImage) || s2.PosterErr != nil {
				t.Errorf("s1 的海报不应影响 s2：%+v", s2)
			}
			if requested := slices.Contains(fake.requested(), "image-s1"); requested != (tt.image != "" || tt.failImage) {
				t.Errorf("请求了 s1 的海报：%v", requested)
			}
		})
	}
}
