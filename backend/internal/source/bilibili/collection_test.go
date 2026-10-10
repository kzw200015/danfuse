package bilibili

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// 用例里的合集 ref。
const (
	bangumiRef   = `{"kind":"bangumi","seasonId":41410}`
	ugcSeasonRef = `{"kind":"ugcSeason","seasonId":8597253,"mid":50329118}`
	multiPageRef = `{"kind":"multiPage","aid":170001}`
)

// collectionView 属于合集 8597253 的稿件 av170001 的 view：pages 个分 P；合集有两个小节，
// 第一节是它自己和一个 3 P 的稿件，第二节是一个单 P 的稿件（分 P 标题与稿件标题相同）。
func collectionView(t *testing.T, pages int) response {
	t.Helper()
	data := viewData{Title: "合集里的稿件", UGCSeason: ewcSeason(pages)}
	for i := range pages {
		data.Pages = append(data.Pages, viewPage{Page: i + 1, CID: int64(101 + i), Part: fmt.Sprintf("第 %d 局", i+1), Duration: 10})
	}
	return dataResponse(t, data)
}

// ewcSeason 合集 8597253 的合集信息，第一个稿件有 pages 个分 P。
func ewcSeason(pages int) *ugcSeason {
	parts := func(prefix string, n int) []ugcPage {
		p := make([]ugcPage, n)
		for i := range n {
			p[i] = ugcPage{Page: i + 1, Part: fmt.Sprintf(" %s %d ", prefix, i+1)}
		}
		return p
	}
	return &ugcSeason{
		ID: 8597253, Mid: 50329118,
		Sections: []ugcSection{
			{Episodes: []ugcEpisode{
				{Aid: 170001, Pages: parts("第", pages)},
				{Aid: 170002, Pages: append(parts("局", 3), ugcPage{Page: 0, Part: "坏的分 P"})},
			}},
			{Episodes: []ugcEpisode{{Aid: 170003, Pages: []ugcPage{{Page: 1, Part: "下一节"}}}}},
		},
	}
}

// ewcArchives 合集 8597253 的条目列表，与 collectionView 里的合集一致。
func ewcArchives(t *testing.T) response {
	t.Helper()
	return dataResponse(t, archivesData{
		Archives: []archive{{Aid: 170001, Title: " 合集里的稿件 "}, {Aid: 170002, Title: "第二个"}, {Aid: 170003, Title: "下一节"}},
		Meta:     archivesMeta{Mid: 50329118, Title: "2026EWC"},
		Page:     archivesPager{Total: 3},
	})
}

func TestParseCollectionLink(t *testing.T) {
	type candidate struct{ kind, ref string }
	bangumi := []candidate{{"bangumi", bangumiRef}}
	ugc := candidate{"ugcSeason", ugcSeasonRef}
	multi := candidate{"multiPage", multiPageRef}
	single := viewResponse(t, "单 P 的稿件", viewPage{Page: 1, CID: 101, Duration: 10})
	tests := []struct {
		name     string
		link     string
		samples  map[string][]response
		want     []candidate
		kind     source.Kind // want 为空时期望的错误
		message  string
		requests []string
	}{
		// 番剧
		{name: "整季 ss 链接：不联网", link: "https://www.bilibili.com/bangumi/play/ss41410?spm_id_from=333.337", want: bangumi},
		{name: "裸 ss 号", link: "ss41410", want: bangumi},
		{
			name: "作品页 md 换算成整季", link: "https://www.bilibili.com/bangumi/media/md28237119/",
			samples: map[string][]response{"media-28237119": {mediaResponse(41410)}}, want: bangumi, requests: []string{"media-28237119"},
		},
		{
			name: "md 换算结果为 0：番剧不存在", link: "md999999999",
			samples: map[string][]response{"media-999999999": {mediaResponse(0)}},
			kind:    source.NotFound, message: "番剧不存在、已下架或不可见", requests: []string{"media-999999999"},
		},
		{
			name: "单集 ep 取它所在的季", link: "https://www.bilibili.com/bangumi/play/ep508404",
			samples: map[string][]response{"pgc-508404": {pgcResponse(t, testSeason())}}, want: bangumi, requests: []string{"pgc-508404"},
		},
		{
			name: "番剧的稿件取它所在的季", link: "https://www.bilibili.com/video/av170001",
			samples: map[string][]response{
				testView:     {viewRedirectResponse(t, "番剧的稿件", "https://www.bilibili.com/bangumi/play/ep600001", viewPage{Page: 1, CID: 301}, viewPage{Page: 2, CID: 302})},
				"pgc-600001": {pgcResponse(t, testSeason())},
			},
			want: bangumi, requests: []string{testView, "pgc-600001"},
		},
		{
			name: "短链指向整季", link: "https://b23.tv/ss41410",
			samples: map[string][]response{"short-b23.tv-ss41410": {redirectResponse("https://www.bilibili.com/bangumi/play/ss41410")}},
			want:    bangumi, requests: []string{"short-b23.tv-ss41410"},
		},
		// 空间里的合集页：mid 取自接口，不取链接里的
		{
			name: "新版合集页", link: "https://space.bilibili.com/1/lists/8597253?type=season&spm_id_from=333.1387",
			samples: map[string][]response{"archives-8597253-1": {ewcArchives(t)}}, want: []candidate{ugc}, requests: []string{"archives-8597253-1"},
		},
		{
			name: "旧版合集页", link: "space.bilibili.com/50329118/channel/collectiondetail?sid=8597253",
			samples: map[string][]response{"archives-8597253-1": {ewcArchives(t)}}, want: []candidate{ugc}, requests: []string{"archives-8597253-1"},
		},
		{
			name: "合集不存在", link: "https://space.bilibili.com/1/lists/999?type=season",
			samples: map[string][]response{"archives-999-1": {codeResponse(-404)}},
			kind:    source.NotFound, message: "合集不存在或已删除", requests: []string{"archives-999-1"},
		},
		// 普通稿件
		{
			name: "多 P 投稿", link: "BV17x411w7KC?p=2",
			samples: map[string][]response{testView: {viewResponse(t, "多 P", viewPage{Page: 1, CID: 101}, viewPage{Page: 2, CID: 102})}},
			want:    []candidate{multi}, requests: []string{testView},
		},
		{
			name: "属于合集的单 P 稿件", link: "https://www.bilibili.com/video/BV17x411w7KC",
			samples: map[string][]response{testView: {collectionView(t, 1)}}, want: []candidate{ugc}, requests: []string{testView},
		},
		{
			name: "多 P 又属于合集：多 P 在前", link: "av170001",
			samples: map[string][]response{testView: {collectionView(t, 5)}}, want: []candidate{multi, ugc}, requests: []string{testView},
		},
		{
			name: "单 P、不属于合集", link: "av170001", samples: map[string][]response{testView: {single}},
			kind: source.InvalidLink, message: "这个稿件只有一个分 P，也不属于合集，请在集面板绑定", requests: []string{testView},
		},
		{
			name: "稿件不存在", link: "av170001", samples: map[string][]response{testView: {codeResponse(-404)}},
			kind: source.NotFound, message: "视频不存在、已删除或不可见", requests: []string{testView},
		},
		{
			name: "限流", link: "av170001", samples: map[string][]response{testView: {codeResponse(-412)}},
			kind: source.RateLimited, message: "B 站限流，请稍后再试", requests: []string{testView, testView, testView, testView},
		},
		// 不支持的
		{name: "新版系列页", link: "https://space.bilibili.com/37737161/lists/2800550?type=series", kind: source.InvalidLink, message: "暂不支持系列"},
		{name: "旧版系列页", link: "https://space.bilibili.com/37737161/channel/seriesdetail?sid=2800550", kind: source.InvalidLink, message: "暂不支持系列"},
		{name: "系列的播放列表", link: "https://www.bilibili.com/list/37737161?sid=2800550", kind: source.InvalidLink, message: "暂不支持系列"},
		{
			name: "没写 type 的列表页", link: "https://space.bilibili.com/2142762/lists/7540520",
			kind: source.InvalidLink, message: "链接里没有写明是合集还是系列，请从合集页重新复制链接",
		},
		{
			name: "短链指向系列", link: "https://b23.tv/abcdefg",
			samples: map[string][]response{"short-b23.tv-abcdefg": {redirectResponse("https://space.bilibili.com/37737161/lists/2800550?type=series")}},
			kind:    source.InvalidLink, message: "暂不支持系列", requests: []string{"short-b23.tv-abcdefg"},
		},
		{
			name: "短链指向认不出的页面", link: "https://b23.tv/abcdefg",
			samples: map[string][]response{"short-b23.tv-abcdefg": {redirectResponse("https://live.bilibili.com/22603245")}},
			kind:    source.InvalidLink, message: "短链指向的不是番剧、合集或投稿", requests: []string{"short-b23.tv-abcdefg"},
		},
		{name: "不是 B 站的链接", link: "https://www.youtube.com/watch?v=dQw4w9WgXcQ", kind: source.InvalidLink, message: "无法识别的链接"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake(t, tt.samples)
			a := fake.adapter()

			adapter, candidates, err := source.NewRegistry(a).ParseCollectionLink(t.Context(), tt.link)

			if tt.want != nil {
				var got []candidate
				for _, c := range candidates {
					got = append(got, candidate{c.Kind, string(c.Ref)})
				}
				if err != nil || adapter != a || !slices.Equal(got, tt.want) {
					t.Errorf("ParseCollectionLink() = (%v, %v), want %v", got, err, tt.want)
				}
			} else {
				assertErrorMessage(t, err, tt.kind, tt.message)
			}
			if got := fake.requested(); !slices.Equal(got, tt.requests) {
				t.Errorf("请求 = %q, want %q", got, tt.requests)
			}
		})
	}
}

// item 期望的合集条目，ref 写成 JSON 字符串。
type item struct {
	ref, label, unmatched string
	number                int
}

func items(c source.Collection) []item {
	got := make([]item, len(c.Items))
	for i, it := range c.Items {
		got[i] = item{ref: string(it.Ref), label: it.Label, unmatched: it.Unmatched, number: it.Number}
	}
	return got
}

// TestListBangumi 番剧只取正片：混在 episodes 里的预告（与正片同号）和 section 里的 PV 都不是条目；
// 整数集号为序号，其余对不上；标签用 show_title，没有时用集号加集标题；完结标志取 publish.is_finish。
func TestListBangumi(t *testing.T) {
	season := seasonData{
		SeasonID: 41410,
		Title:    " 某番剧 ",
		Episodes: []pgcEpisode{
			{ID: 1, Title: "1", ShowTitle: "第1话 开始", SectionType: 0},
			{ID: 2, Title: "2", ShowTitle: "第2话预告", SectionType: 1}, // 预告排在正片之前，与正片同号
			{ID: 3, Title: "2", LongTitle: "继续", SectionType: 0},    // 没有 show_title
			{ID: 4, Title: "24.9", ShowTitle: "第24.9话 闲话", SectionType: 0},
			{ID: 5, Title: "SP", ShowTitle: "SP 总集篇", SectionType: 0},
			{ID: 6, Title: "OAD02", ShowTitle: "OAD02", SectionType: 0},
			{ID: 7, Title: "03", ShowTitle: "第3话", SectionType: 0},
		},
		Section: []pgcSection{{Episodes: []pgcEpisode{{ID: 8, Title: "PV1", ShowTitle: "PV1", SectionType: 2}}}},
	}
	ep := func(id int) string { return fmt.Sprintf(`{"kind":"episode","epId":%d}`, id) }
	want := []item{
		{ref: ep(1), label: "第1话 开始", number: 1},
		{ref: ep(3), label: "2 继续", number: 2},
		{ref: ep(4), label: "第24.9话 闲话", unmatched: "集号「24.9」不是整数"},
		{ref: ep(5), label: "SP 总集篇", unmatched: "集号「SP」不是整数"},
		{ref: ep(6), label: "OAD02", unmatched: "集号「OAD02」不是整数"},
		{ref: ep(7), label: "第3话", number: 3},
	}
	for _, finished := range []int{0, 1} {
		t.Run(fmt.Sprintf("is_finish=%d", finished), func(t *testing.T) {
			season.Publish.IsFinish = finished
			fake := newFake(t, map[string][]response{"season-41410": {pgcResponse(t, season)}})

			got, err := fake.adapter().ListCollection(t.Context(), source.CollectionRef(bangumiRef))

			if err != nil || got.Title != "某番剧" || got.Finished != (finished == 1) {
				t.Fatalf("ListCollection() = (%q, 完结 %v, %v), want (某番剧, %v)", got.Title, got.Finished, err, finished == 1)
			}
			if !reflect.DeepEqual(items(got), want) {
				t.Errorf("条目 = %+v\nwant %+v", items(got), want)
			}
		})
	}
}

// TestListUGCSeason 投稿合集展开到分 P：按条目列表的顺序、每个稿件按分 P 号，标签为"稿件标题 / 分 P 标题"
// （分 P 标题为空或与稿件标题相同时只有稿件标题），序号留给集号规则；分 P 取自合集里稿件的 view，第一个取不到时换下一个。
func TestListUGCSeason(t *testing.T) {
	video := func(aid, page int) string { return fmt.Sprintf(`{"kind":"video","aid":%d,"page":%d}`, aid, page) }
	all := []item{
		{ref: video(170001, 1), label: "合集里的稿件 / 第 1"},
		{ref: video(170001, 2), label: "合集里的稿件 / 第 2"},
		{ref: video(170002, 1), label: "第二个 / 局 1"},
		{ref: video(170002, 2), label: "第二个 / 局 2"},
		{ref: video(170002, 3), label: "第二个 / 局 3"},
		{ref: video(170003, 1), label: "下一节"},
	}
	otherSeason := ewcSeason(2)
	otherSeason.ID = 1
	tests := []struct {
		name     string
		views    map[string][]response
		want     []item
		wantErr  source.Kind
		requests []string
	}{
		{
			name:     "展开各稿件的分 P",
			views:    map[string][]response{testView: {collectionView(t, 2)}},
			want:     all,
			requests: []string{"archives-8597253-1", testView},
		},
		{
			name: "第一个稿件刚被删除、第二个的 view 里不是这个合集：换下一个稿件取 view",
			views: map[string][]response{
				testView:      {codeResponse(-404)},
				"view-170002": {dataResponse(t, viewData{Title: "第二个", UGCSeason: otherSeason})},
				"view-170003": {dataResponse(t, viewData{Title: "下一节", UGCSeason: ewcSeason(2)})},
			},
			want:     all,
			requests: []string{"archives-8597253-1", testView, "view-170002", "view-170003"},
		},
		{
			name: "合集里的稿件都取不到 view：上游错误",
			views: map[string][]response{
				testView:      {codeResponse(-404)},
				"view-170002": {codeResponse(-404)},
				"view-170003": {dataResponse(t, viewData{Title: "下一节"})},
			},
			wantErr:  source.Upstream,
			requests: []string{"archives-8597253-1", testView, "view-170002", "view-170003"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			samples := map[string][]response{"archives-8597253-1": {ewcArchives(t)}}
			maps.Copy(samples, tt.views)
			fake := newFake(t, samples)

			got, err := fake.adapter().ListCollection(t.Context(), source.CollectionRef(ugcSeasonRef))

			if tt.wantErr != 0 {
				if srcErr, ok := errors.AsType[*source.Error](err); !ok || srcErr.Kind != tt.wantErr {
					t.Fatalf("ListCollection() error = %v, want Kind %v", err, tt.wantErr)
				}
			} else {
				if err != nil || got.Title != "2026EWC" || got.Finished || !got.NumberedByRule {
					t.Fatalf("ListCollection() = (%q, 完结 %v, 按规则编号 %v, %v), want (2026EWC, 没有完结标志, 按规则编号)",
						got.Title, got.Finished, got.NumberedByRule, err)
				}
				if !reflect.DeepEqual(items(got), tt.want) {
					t.Errorf("条目 = %+v\nwant %+v", items(got), tt.want)
				}
			}
			if req := fake.requested(); !slices.Equal(req, tt.requests) {
				t.Errorf("请求 = %q, want %q", req, tt.requests)
			}
		})
	}
}

// TestListUGCSeasonPaging 条目超过一页（100 条）时翻页列全，顺序接着上一页；view 里没有的稿件只有 P1，标签为稿件标题。
func TestListUGCSeasonPaging(t *testing.T) {
	page := func(from, to, total int) response {
		data := archivesData{Meta: archivesMeta{Mid: 1, Title: "大合集"}, Page: archivesPager{Total: total}}
		for aid := from; aid <= to; aid++ {
			data.Archives = append(data.Archives, archive{Aid: int64(aid), Title: fmt.Sprintf("第 %d 期", aid)})
		}
		return dataResponse(t, data)
	}
	season := &ugcSeason{ID: 8597253, Mid: 1, Sections: []ugcSection{{Episodes: []ugcEpisode{{Aid: 1, Pages: []ugcPage{{Page: 1}}}}}}}
	fake := newFake(t, map[string][]response{
		"archives-8597253-1": {page(1, 100, 230)},
		"archives-8597253-2": {page(101, 200, 230)},
		"archives-8597253-3": {page(201, 230, 230)},
		"view-1":             {dataResponse(t, viewData{Title: "第 1 期", UGCSeason: season})},
	})

	got, err := fake.adapter().ListCollection(t.Context(), source.CollectionRef(ugcSeasonRef))

	if err != nil || len(got.Items) != 230 {
		t.Fatalf("ListCollection() = (%d 条, %v), want 230 条", len(got.Items), err)
	}
	for i, it := range got.Items {
		if string(it.Ref) != fmt.Sprintf(`{"kind":"video","aid":%d,"page":1}`, i+1) || it.Label != fmt.Sprintf("第 %d 期", i+1) {
			t.Fatalf("第 %d 条 = %+v", i+1, it)
		}
	}
	want := []string{"archives-8597253-1", "archives-8597253-2", "archives-8597253-3", "view-1"}
	if req := fake.requested(); !slices.Equal(req, want) {
		t.Errorf("请求 = %q, want %q", req, want)
	}
}

// TestListMultiPage 多 P 投稿的各个分 P 是条目，标签为"稿件标题 / 分 P 标题"，序号留给集号规则。
func TestListMultiPage(t *testing.T) {
	fake := newFake(t, map[string][]response{testView: {viewResponse(t, " 合辑 ",
		viewPage{Page: 1, CID: 101, Part: "第一首"},
		viewPage{Page: 2, CID: 102, Part: " "},
		viewPage{Page: 3, CID: 103, Part: "合辑"},
		viewPage{Page: 0, CID: 104, Part: "坏的分 P"},
	)}})

	got, err := fake.adapter().ListCollection(t.Context(), source.CollectionRef(multiPageRef))

	page := func(p int) string { return fmt.Sprintf(`{"kind":"video","aid":170001,"page":%d}`, p) }
	want := []item{
		{ref: page(1), label: "合辑 / 第一首"},
		{ref: page(2), label: "合辑"},
		{ref: page(3), label: "合辑"},
	}
	if err != nil || got.Title != "合辑" || got.Finished || !got.NumberedByRule || !reflect.DeepEqual(items(got), want) {
		t.Errorf("ListCollection() = (%q, 按规则编号 %v, %+v, %v)\nwant (合辑, 按规则编号, %+v)", got.Title, got.NumberedByRule, items(got), err, want)
	}
}

// TestListCollectionErrors 合集不存在为 NotFound，提示按合集的种类；限流、接口异常照常重试后归类。
func TestListCollectionErrors(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		sample   string
		resp     response
		kind     source.Kind
		message  string
		attempts int
	}{
		{"番剧不存在", bangumiRef, "season-41410", codeResponse(-404), source.NotFound, "番剧不存在、已下架或不可见", 1},
		{"番剧限流", bangumiRef, "season-41410", codeResponse(-412), source.RateLimited, "B 站限流，请稍后再试", 4},
		{"番剧接口异常", bangumiRef, "season-41410", statusResponse(http.StatusBadGateway), source.Upstream, "B 站接口异常", 4},
		{"合集不存在", ugcSeasonRef, "archives-8597253-1", codeResponse(-404), source.NotFound, "合集不存在或已删除", 1},
		{"合集限流", ugcSeasonRef, "archives-8597253-1", statusResponse(http.StatusPreconditionFailed), source.RateLimited, "B 站限流，请稍后再试", 4},
		{"多 P 投稿不存在", multiPageRef, testView, codeResponse(62002), source.NotFound, "视频不存在、已删除或不可见", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake(t, map[string][]response{tt.sample: {tt.resp}})

			_, err := fake.adapter().ListCollection(t.Context(), source.CollectionRef(tt.ref))

			assertErrorMessage(t, err, tt.kind, tt.message)
			if n := countOf(fake.requested(), tt.sample); n != tt.attempts {
				t.Errorf("%s 被请求 %d 次，want %d", tt.sample, n, tt.attempts)
			}
		})
	}
}

func TestDescribeCollection(t *testing.T) {
	a := New(config.Bilibili{}, discardLogger)
	tests := []struct {
		ref  string
		want source.Display
	}{
		{bangumiRef, source.Display{URL: "https://www.bilibili.com/bangumi/play/ss41410", Label: "B 站番剧 ss41410"}},
		{ugcSeasonRef, source.Display{URL: "https://space.bilibili.com/50329118/lists/8597253?type=season", Label: "B 站投稿合集 8597253"}},
		{multiPageRef, source.Display{URL: "https://www.bilibili.com/video/BV17x411w7KC", Label: "B 站多 P 投稿 BV17x411w7KC"}},
	}
	for _, tt := range tests {
		got, err := a.DescribeCollection(source.CollectionRef(tt.ref))
		if err != nil || got != tt.want {
			t.Errorf("DescribeCollection(%s) = (%+v, %v), want %+v", tt.ref, got, err, tt.want)
		}
	}
}

// TestDescribeCollectionRoundTrip 展示出的链接再识别一次，得到相同的合集 ref。
func TestDescribeCollectionRoundTrip(t *testing.T) {
	fake := newFake(t, map[string][]response{
		"archives-8597253-1": {ewcArchives(t)},
		testView:             {viewResponse(t, "多 P", viewPage{Page: 1, CID: 101}, viewPage{Page: 2, CID: 102})},
	})
	a := fake.adapter()
	for _, ref := range []string{bangumiRef, ugcSeasonRef, multiPageRef} {
		d, err := a.DescribeCollection(source.CollectionRef(ref))
		if err != nil {
			t.Fatalf("DescribeCollection(%s): %v", ref, err)
		}
		candidates, err := a.ParseCollectionLink(t.Context(), d.URL)
		if err != nil || len(candidates) != 1 || string(candidates[0].Ref) != ref {
			t.Errorf("ParseCollectionLink(%q) = (%+v, %v), want %s", d.URL, candidates, err, ref)
		}
	}
}

// TestInvalidCollectionRef 数据库里的合集 ref 不是这个适配器能处理的：列出、描述都返回错误，不请求上游。
func TestInvalidCollectionRef(t *testing.T) {
	fake := newFake(t, nil)
	a := fake.adapter()
	for _, ref := range []string{
		`not json`,
		`{}`,
		`{"kind":"series","seasonId":1}`,
		`{"kind":"bangumi"}`,
		`{"kind":"bangumi","seasonId":41410,"mid":1}`,
		`{"kind":"ugcSeason","seasonId":8597253}`,
		`{"kind":"ugcSeason","mid":1}`,
		`{"kind":"multiPage","aid":0}`,
		`{"kind":"multiPage","aid":170001,"seasonId":1}`,
		`{"kind":"multiPage","aid":2251799813685248}`,
		`{"kind":"video","aid":170001,"page":1}`, // 弹幕源的 ref 不是合集 ref
	} {
		if d, err := a.DescribeCollection(source.CollectionRef(ref)); err == nil {
			t.Errorf("DescribeCollection(%s) = %+v, want error", ref, d)
		}
		if _, err := a.ListCollection(t.Context(), source.CollectionRef(ref)); err == nil || strings.Contains(err.Error(), "B 站") {
			t.Errorf("ListCollection(%s) error = %v, want 不是 B 站接口的错误", ref, err)
		}
	}
	if got := fake.requested(); len(got) != 0 {
		t.Errorf("不应请求上游，请求了 %q", got)
	}
}
