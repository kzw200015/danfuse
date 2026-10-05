package bilibili

import (
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// testSeason 构造的番剧：两集正片，section 里两个 PV。PV 是同一个稿件的两个分 P，见 TestFetchRedirect。
func testSeason() seasonData {
	return seasonData{
		SeasonID: 41410,
		Title:    " 某番剧 第二季 ",
		Episodes: []pgcEpisode{
			{ID: 508404, CID: 201, Title: "1", LongTitle: "第一集的标题", ShowTitle: "第1话 第一集的标题", Duration: 1451000},
			{ID: 508405, CID: 202, Title: "2", LongTitle: "第二集的标题", Duration: 1440499}, // 没有 show_title
		},
		Section: []pgcSection{{Episodes: []pgcEpisode{
			{ID: 600001, CID: 301, Title: "先导PV", ShowTitle: "先导PV", Duration: 52000},
			{ID: 600002, CID: 302, Title: "正式PV", ShowTitle: "正式PV", Duration: 132500},
		}}},
	}
}

func TestFetchEpisode(t *testing.T) {
	tests := []struct {
		name     string
		ep       string // 番剧单集的链接，也是 pgc 样本的 ep_id
		cid      int64
		title    string
		duration int
	}{
		{"正片：标题为番剧名加 show_title，毫秒四舍五入到秒", "508404", 201, "某番剧 第二季 第1话 第一集的标题", 1451},
		{"正片：没有 show_title 时用集号加集标题", "508405", 202, "某番剧 第二季 2 第二集的标题", 1440},
		{"section 里的 PV", "600002", 302, "某番剧 第二季 正式PV", 133},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cid := strconv.FormatInt(tt.cid, 10)
			samples := map[string][]response{
				"pgc-" + tt.ep: {pgcResponse(t, testSeason())},
				"xml-" + cid:   {emptyXML(t)},
			}
			for n := 1; n <= 5; n++ {
				samples[fmt.Sprintf("seg-%s-%d", cid, n)] = []response{segResponse()}
			}
			samples["seg-"+cid+"-1"] = []response{segResponse(elem{id: 11, progress: 1000, mode: 1, content: "第一段"}.encode())}
			fake := newFake(t, samples)

			got, err := fetch(t, fake.adapter(), "https://www.bilibili.com/bangumi/play/ep"+tt.ep)
			if err != nil {
				t.Fatal(err)
			}
			want := source.Fetched{
				Title:    tt.title,
				Duration: tt.duration,
				Danmaku:  []danmaku.Danmaku{{SourceID: 11, TimeMs: 1000, Mode: danmaku.ModeScroll, Text: "第一段"}},
				LogAttrs: []slog.Attr{slog.Int64("cid", tt.cid), slog.Int("protobuf", 1), slog.Int("xml", 0), slog.Int("overlap", 0)},
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Fetch() = %+v\nwant %+v", got, want)
			}
			if req := fake.requested(); req[0] != "pgc-"+tt.ep || countPrefix(req, "seg-") != (tt.duration+359)/360 {
				t.Errorf("请求 = %q, want 先取元数据，再取 XML 和 ceil(时长 / 360) 段", req)
			}
		})
	}
}

// TestFetchEpisodeNotFound 番剧单集不存在时与投稿用同一个提示。
func TestFetchEpisodeNotFound(t *testing.T) {
	const message = "视频不存在、已删除或不可见"
	tests := []struct {
		name string
		pgc  response
	}{
		{"pgc -404", codeResponse(-404)},
		{"ep 不在这一季的列表里", pgcResponse(t, seasonData{SeasonID: 1, Title: "某番剧", Episodes: []pgcEpisode{{ID: 1, CID: 2}}})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake(t, map[string][]response{"pgc-409059": {tt.pgc}})

			_, err := fetch(t, fake.adapter(), "ep409059")

			assertErrorMessage(t, err, source.NotFound, message)
			if got := fake.requested(); !slices.Equal(got, []string{"pgc-409059"}) {
				t.Errorf("请求 = %q, want 只取元数据", got)
			}
		})
	}
}

func TestFetchEpisodeErrors(t *testing.T) {
	noCID := testSeason()
	noCID.Episodes[0].CID = 0
	tests := []struct {
		name     string
		pgc      response
		kind     source.Kind
		attempts int
	}{
		{"pgc -403", codeResponse(-403), source.AuthRequired, 1},
		{"pgc -352", codeResponse(-352), source.RateLimited, 4},
		{"pgc HTTP 500", statusResponse(http.StatusInternalServerError), source.Upstream, 4},
		{"pgc result 结构不对", jsonResponse(`{"code":0,"result":{"episodes":"x"}}`), source.Upstream, 4},
		{"单集没有 cid：不重试", pgcResponse(t, noCID), source.Upstream, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake(t, map[string][]response{"pgc-508404": {tt.pgc}})

			_, err := fetch(t, fake.adapter(), "ep508404")

			assertError(t, err, tt.kind)
			if n := countOf(fake.requested(), "pgc-508404"); n != tt.attempts {
				t.Errorf("pgc 被请求 %d 次，want %d", n, tt.attempts)
			}
		})
	}
}

// TestFetchRedirect 带 redirect_url 的投稿按番剧处理：在整季里按分 P 的 cid 找到对应的那一集。
func TestFetchRedirect(t *testing.T) {
	// 番剧的稿件有两个分 P，是 section 里的两个 PV；redirect_url 只指向第一个
	pvView := viewRedirectResponse(t, "【10月】某番剧 PV", "https://www.bilibili.com/bangumi/play/ep600001",
		viewPage{Page: 1, CID: 301, Part: "先导PV.encoded", Duration: 52},
		viewPage{Page: 2, CID: 302, Part: "正式PV.encoded", Duration: 133},
	)
	tests := []struct {
		name     string
		link     string
		view     response
		cid      string
		title    string
		duration int
		requests []string
	}{
		{
			name: "P1 是 redirect_url 指向的那一集", link: "av170001", view: pvView, cid: "301",
			title: "某番剧 第二季 先导PV", duration: 52, requests: []string{testView, "pgc-600001", "seg-301-1", "xml-301"},
		},
		{
			name: "P2 按 cid 找到另一集", link: "av170001?p=2", view: pvView, cid: "302",
			title: "某番剧 第二季 正式PV", duration: 133, requests: []string{testView, "pgc-600001", "seg-302-1", "xml-302"},
		},
		{
			name: "整季里没有这个 cid：按普通投稿处理", link: "av170001",
			view: viewRedirectResponse(t, "番剧的稿件", "https://www.bilibili.com/bangumi/play/ep600001", viewPage{Page: 1, CID: 999, Part: "x", Duration: 10}),
			cid:  "999", title: "番剧的稿件", duration: 10, requests: []string{testView, "pgc-600001", "seg-999-1", "xml-999"},
		},
		{
			name: "redirect_url 不是番剧单集：按普通投稿处理，不取番剧的元数据", link: "av170001",
			view: viewRedirectResponse(t, "课程", "https://www.bilibili.com/cheese/play/ep1234", viewPage{Page: 1, CID: 999, Part: "x", Duration: 10}),
			cid:  "999", title: "课程", duration: 10, requests: []string{testView, "seg-999-1", "xml-999"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake(t, map[string][]response{
				testView:               {tt.view},
				"pgc-600001":           {pgcResponse(t, testSeason())},
				"seg-" + tt.cid + "-1": {segResponse()},
				"xml-" + tt.cid:        {emptyXML(t)},
			})

			got, err := fetch(t, fake.adapter(), tt.link)

			if err != nil || got.Title != tt.title || got.Duration != tt.duration {
				t.Errorf("Fetch() = (%q, %d 秒, %v), want (%q, %d 秒)", got.Title, got.Duration, err, tt.title, tt.duration)
			}
			if req := slices.Sorted(slices.Values(fake.requested())); !slices.Equal(req, slices.Sorted(slices.Values(tt.requests))) {
				t.Errorf("请求 = %q, want %q", req, tt.requests)
			}
		})
	}
}

// TestFetchRedirectNotFound 番剧的稿件取它所在的季时 NotFound，同样提示视频不存在。
func TestFetchRedirectNotFound(t *testing.T) {
	fake := newFake(t, map[string][]response{
		testView:     {viewRedirectResponse(t, "番剧的稿件", "https://www.bilibili.com/bangumi/play/ep600001", viewPage{Page: 1, CID: 301, Duration: 52})},
		"pgc-600001": {codeResponse(-404)},
	})

	_, err := fetch(t, fake.adapter(), "av170001")

	assertErrorMessage(t, err, source.NotFound, "视频不存在、已删除或不可见")
}
