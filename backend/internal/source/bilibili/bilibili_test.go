package bilibili

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// testView 构造的用例都拉取 av170001（BV17x411w7KC），view 的样本名。
const testView = "view-170001"

// fetch 用 link 解析出的 ref 拉取。
func fetch(t *testing.T, a *Adapter, link string) (source.Fetched, error) {
	t.Helper()
	ref, err := a.ParseLink(t.Context(), link)
	if err != nil {
		t.Fatalf("ParseLink(%q) error = %v", link, err)
	}
	return a.Fetch(t.Context(), ref)
}

// assertError 断言 err 是指定类别的 *source.Error，提示文字按错误归类表。
func assertError(t *testing.T, err error, kind source.Kind) {
	t.Helper()
	assertErrorMessage(t, err, kind, map[source.Kind]string{
		source.NotFound:     "视频不存在、已删除或不可见",
		source.AuthRequired: "需要登录，请配置 SESSDATA",
		source.RateLimited:  "B 站限流，请稍后再试",
		source.Upstream:     "B 站接口异常",
	}[kind])
}

// assertErrorMessage 断言 err 是指定类别和提示文字的 *source.Error。
func assertErrorMessage(t *testing.T, err error, kind source.Kind, wantMessage string) {
	t.Helper()
	srcErr, ok := errors.AsType[*source.Error](err)
	if !ok {
		t.Fatalf("error = %v, want *source.Error", err)
	}
	if srcErr.Kind != kind || srcErr.Message != wantMessage {
		t.Errorf("error = {Kind: %d, Message: %q}, want {Kind: %d, Message: %q}（%v）", srcErr.Kind, srcErr.Message, kind, wantMessage, err)
	}
}

// TestParseLink 不是短链的链接只做字符串解析，不请求上游。
func TestParseLink(t *testing.T) {
	const p1, p2, p3 = `{"kind":"video","aid":170001,"page":1}`, `{"kind":"video","aid":170001,"page":2}`, `{"kind":"video","aid":170001,"page":3}`
	const ep = `{"kind":"episode","epId":508404}`
	const wholeSeason = "整季或合集的链接请在季面板绑定"
	tests := []struct {
		link string
		want string // 规范化的 ref；不是 JSON 时为期望的 InvalidLink 提示，为空表示"无法识别的链接"
	}{
		// 各种写法都规范化为相同的 ref
		{"https://www.bilibili.com/video/BV17x411w7KC", p1},
		{"https://www.bilibili.com/video/BV17x411w7KC/", p1},
		{"https://www.bilibili.com/video/BV17x411w7KC/?p=1&spm_id_from=333.788.videopod.episodes", p1},
		{"https://www.bilibili.com/video/BV17x411w7KC#reply1234", p1},
		{"http://bilibili.com/video/av170001", p1},
		{"https://m.bilibili.com/video/av170001/", p1},
		{"HTTPS://WWW.BILIBILI.COM/video/BV17x411w7KC", p1},
		{"www.bilibili.com/video/BV17x411w7KC", p1},
		{"bilibili.com/video/av170001?p=1", p1},
		{"BV17x411w7KC", p1},
		{"av170001", p1},
		{"  av170001\n", p1},
		{"https://www.bilibili.com/video/BV17x411w7KC?p=2", p2},
		{"https://www.bilibili.com/video/av170001/?spm_id_from=333.1007&p=2", p2},
		{"https://m.bilibili.com/video/BV17x411w7KC?p=3", p3},
		{"BV17x411w7KC?p=3", p3},
		{"av170001?p=3", p3},
		// BV 号在本地换算成 aid
		{"BV1ZY4y187fA", `{"kind":"video","aid":641107054,"page":1}`},
		{"BV1xx411c7XX", `{"kind":"video","aid":294,"page":1}`},
		{"av2251799813685247", `{"kind":"video","aid":2251799813685247,"page":1}`}, // 最大的 aid
		// 番剧单集，查询串忽略
		{"https://www.bilibili.com/bangumi/play/ep508404", ep},
		{"https://www.bilibili.com/bangumi/play/ep508404/?from_spmid=666.25.episode.0&p=2", ep},
		{"https://m.bilibili.com/bangumi/play/ep508404", ep},
		{"http://bilibili.com/bangumi/play/ep508404", ep},
		{"www.bilibili.com/bangumi/play/ep508404", ep},
		{"ep508404", ep},
		{" ep508404?p=3 ", ep},
		// 整季、作品页、空间里的合集页定位不到单集，到季面板绑定
		{"https://www.bilibili.com/bangumi/play/ss41410", wholeSeason},
		{"https://m.bilibili.com/bangumi/play/ss41410/?spm_id_from=333.337", wholeSeason},
		{"https://www.bilibili.com/bangumi/media/md28237119", wholeSeason},
		{"https://www.bilibili.com/bangumi/media/md28237119/", wholeSeason},
		{"ss41410", wholeSeason},
		{"md28237119", wholeSeason},
		{"https://space.bilibili.com/2142762/lists/7540520?type=season", wholeSeason},
		{"https://space.bilibili.com/2142762/lists/7540520/?type=season&spm_id_from=333.1387", wholeSeason},
		{"space.bilibili.com/2142762/channel/collectiondetail?sid=7540520", wholeSeason},
		// 系列页在集面板与其他链接一样认不出
		{"https://space.bilibili.com/37737161/lists/2800550?type=series", ""},
		{"https://space.bilibili.com/37737161/channel/seriesdetail?sid=2800550", ""},
		{"https://www.bilibili.com/list/37737161?sid=2800550", ""},
		{"https://space.bilibili.com/2142762/lists/7540520", ""}, // 没写 type，分不出合集和系列
		// 无法识别
		{"", ""},
		{"   ", ""},
		{"你好", ""},
		{"170001", ""},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", ""},
		{"https://evil.example/video/BV17x411w7KC", ""},
		{"https://www.bilibili.com.evil.example/video/BV17x411w7KC", ""},
		{"ftp://www.bilibili.com/video/BV17x411w7KC", ""},
		{"https://www.bilibili.com/", ""},
		{"https://www.bilibili.com/video/", ""},
		{"https://www.bilibili.com/video/BV17x411w7KC/extra", ""},
		{"https://space.bilibili.com/video/BV17x411w7KC", ""},
		{"https://www.bilibili.com/video/ep508404", ""},
		{"https://www.bilibili.com/bangumi/play/BV17x411w7KC", ""},
		{"https://www.bilibili.com/bangumi/play/md28237119", ""},
		{"https://www.bilibili.com/bangumi/media/ss41410", ""},
		{"https://www.bilibili.com/bangumi/play/ep508404/extra", ""},
		{"https://www.bilibili.com/bangumi/play/", ""},
		{"https://live.bilibili.com/22603245", ""},
		{"https://space.bilibili.com/2142762", ""},
		{"https://space.bilibili.com/2142762/lists/7540520?type=other", ""},
		{"https://space.bilibili.com/abc/lists/7540520?type=season", ""},
		{"https://space.bilibili.com/2142762/lists/0?type=season", ""},
		{"https://space.bilibili.com/2142762/channel/collectiondetail", ""},
		{"https://space.bilibili.com/2142762/channel/collectiondetail?sid=x", ""},
		{"https://www.bilibili.com/list/37737161", ""},
		{"https://www.bilibili.com/list/watchlater?sid=1", ""},
		{"ep", ""},
		{"ep0", ""},
		{"ep-1", ""},
		{"ep12a", ""},
		{"EP508404", ""},
		{"ss", ""},
		{"ss0", ""},
		{"mdabc", ""},
		{"SS41410", ""},
		{"https://b23.tv/", ""}, // 短链要有路径
		{"https://t.cn/A6abcdef", ""},
		{"ftp://b23.tv/ep508404", ""},
		{"BV17x411w7K", ""},   // 少一位
		{"BV17x411w7KCC", ""}, // 多一位
		{"BV17x411w7K0", ""},  // 0 不在码表里
		{"bv17x411w7KC", ""},
		{"BV27x411w7KC", ""}, // 不是 BV1 开头
		{"BV17x411wZKC", ""}, // 能解出数字，但换算不回原样
		{"av0", ""},
		{"av", ""},
		{"av-1", ""},
		{"av+1", ""},
		{"av1x", ""},
		{"AV170001", ""},
		{"av2251799813685248", ""}, // 超出 BV 号能表示的范围
		{"av99999999999999999999", ""},
		{"https://www.bilibili.com/video/av170001?p=0", ""},
		{"https://www.bilibili.com/video/av170001?p=-1", ""},
		{"https://www.bilibili.com/video/av170001?p=abc", ""},
		{"https://www.bilibili.com/video/av170001?p=", ""},
	}
	for _, tt := range tests {
		t.Run(tt.link, func(t *testing.T) {
			fake := newFake(t, nil)
			a := fake.adapter()

			adapter, ref, err := source.NewRegistry(a).ParseLink(t.Context(), tt.link)

			if !strings.HasPrefix(tt.want, "{") {
				reject := cmp.Or(tt.want, "无法识别的链接")
				srcErr, ok := errors.AsType[*source.Error](err)
				if !ok || srcErr.Kind != source.InvalidLink || srcErr.Message != reject {
					t.Errorf("ParseLink() = (%s, %v), want InvalidLink「%s」", ref, err, reject)
				}
			} else if err != nil || adapter != a || string(ref) != tt.want {
				t.Errorf("ParseLink() = (%s, %v), want %s", ref, err, tt.want)
			}
			if got := fake.requested(); len(got) != 0 {
				t.Errorf("链接解析不应请求上游，请求了 %q", got)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	a := New(config.Bilibili{})
	tests := []struct {
		ref  string
		want source.Display
	}{
		{`{"kind":"video","aid":170001,"page":1}`, source.Display{URL: "https://www.bilibili.com/video/BV17x411w7KC", Label: "B 站投稿 BV17x411w7KC"}},
		{`{"kind":"video","aid":170001,"page":3}`, source.Display{URL: "https://www.bilibili.com/video/BV17x411w7KC?p=3", Label: "B 站投稿 BV17x411w7KC P3"}},
		{`{"aid":641107054,"page":12,"kind":"video"}`, source.Display{URL: "https://www.bilibili.com/video/BV1ZY4y187fA?p=12", Label: "B 站投稿 BV1ZY4y187fA P12"}},
		{`{"kind":"video","aid":294,"page":1}`, source.Display{URL: "https://www.bilibili.com/video/BV1xx411c7XX", Label: "B 站投稿 BV1xx411c7XX"}},
		{`{"kind":"episode","epId":508404}`, source.Display{URL: "https://www.bilibili.com/bangumi/play/ep508404", Label: "B 站番剧 ep508404"}},
	}
	for _, tt := range tests {
		got, err := a.Describe(source.Ref(tt.ref))
		if err != nil || got != tt.want {
			t.Errorf("Describe(%s) = (%+v, %v), want %+v", tt.ref, got, err, tt.want)
		}
	}
}

// TestDescribeRoundTrip 由链接解析出的 ref 展示出的链接，再解析一次得到相同的 ref。
func TestDescribeRoundTrip(t *testing.T) {
	a := New(config.Bilibili{})
	for _, link := range []string{"av170001", "av170001?p=7", "BV1ZY4y187fA", "av1", "av2251799813685247", "ep508404", "ep1"} {
		ref, err := a.ParseLink(t.Context(), link)
		if err != nil {
			t.Fatalf("ParseLink(%q) error = %v", link, err)
		}
		d, err := a.Describe(ref)
		if err != nil {
			t.Fatalf("Describe(%s) error = %v", ref, err)
		}
		again, err := a.ParseLink(t.Context(), d.URL)
		if err != nil || string(again) != string(ref) {
			t.Errorf("ParseLink(%q) = (%s, %v), want %s", d.URL, again, err, ref)
		}
	}
}

// TestInvalidRef 数据库里的 ref 不是这个适配器能处理的：Describe、Fetch 都返回错误，不请求上游。
func TestInvalidRef(t *testing.T) {
	fake := newFake(t, nil)
	a := fake.adapter()
	for _, ref := range []string{
		`not json`,
		`{}`,
		`{"kind":"bangumi","epId":508404}`,
		`{"kind":"episode"}`,
		`{"kind":"episode","epId":0}`,
		`{"kind":"episode","epId":508404,"aid":170001}`,
		`{"kind":"episode","epId":508404,"page":1}`,
		`{"kind":"video","aid":170001,"page":1,"epId":508404}`,
		`{"kind":"video","aid":0,"page":1}`,
		`{"kind":"video","aid":170001}`,
		`{"kind":"video","aid":170001,"page":0}`,
		`{"kind":"video","aid":2251799813685248,"page":1}`,
	} {
		if d, err := a.Describe(source.Ref(ref)); err == nil {
			t.Errorf("Describe(%s) = %+v, want error", ref, d)
		}
		if _, err := a.Fetch(t.Context(), source.Ref(ref)); err == nil {
			t.Errorf("Fetch(%s) error = nil, want error", ref)
		}
	}
	if got := fake.requested(); len(got) != 0 {
		t.Errorf("不应请求上游，请求了 %q", got)
	}
}

func TestFetch(t *testing.T) {
	fake := newFake(t, map[string][]response{
		testView: {viewResponse(t, " 合辑 ",
			viewPage{Page: 1, CID: 101, Part: "第一首", Duration: 199},
			viewPage{Page: 2, CID: 102, Part: "第二首", Duration: 721}, // 3 段，最后一段只有 1 秒
		)},
		"xml-102": {emptyXML(t)},
		"seg-102-1": {segResponse(
			elem{id: 11, progress: 1000, mode: 1, color: 0xFFFFFF, content: "第一段"}.encode(),
			elem{id: 12, progress: 359999, mode: 4, content: "段尾"}.encode(),
		)},
		"seg-102-2": {{status: http.StatusNotModified}},
		"seg-102-3": {segResponse(elem{id: 31, progress: 720500, mode: 5, color: 0x00FF00, content: "最后"}.encode())},
	})

	got, err := fetch(t, fake.adapter(), "https://www.bilibili.com/video/BV17x411w7KC?p=2")
	if err != nil {
		t.Fatal(err)
	}

	want := source.Fetched{
		Title:    "合辑 / 第二首",
		Duration: 721,
		Danmaku: []danmaku.Danmaku{
			{SourceID: 11, TimeMs: 1000, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "第一段"},
			{SourceID: 12, TimeMs: 359999, Mode: danmaku.ModeBottom, Text: "段尾"},
			{SourceID: 31, TimeMs: 720500, Mode: danmaku.ModeTop, Color: 0x00FF00, Text: "最后"},
		},
		LogAttrs: []slog.Attr{slog.Int64("cid", 102), slog.Int("protobuf", 3), slog.Int("xml", 0), slog.Int("overlap", 0)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Fetch() = %+v\nwant %+v", got, want)
	}
	if req := fake.requested(); len(req) != 5 || req[0] != testView {
		t.Errorf("请求 = %q, want 先取元数据，再取 XML 和 3 段", req)
	}
}

func TestFetchTitle(t *testing.T) {
	tests := []struct {
		name  string
		pages []viewPage
		want  string
	}{
		{"多 P：视频标题 / 分 P 标题", []viewPage{{Page: 1, CID: 101, Part: " 第一首 "}, {Page: 2, CID: 102}}, "视频 / 第一首"},
		{"多 P，分 P 标题为空", []viewPage{{Page: 1, CID: 101, Part: " "}, {Page: 2, CID: 102}}, "视频"},
		{"只有一个分 P：只用视频标题", []viewPage{{Page: 1, CID: 101, Part: "upload_final.mp4"}}, "视频"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake(t, map[string][]response{
				testView:    {viewResponse(t, " 视频 ", tt.pages...)},
				"seg-101-1": {segResponse()},
				"xml-101":   {emptyXML(t)},
			})
			got, err := fetch(t, fake.adapter(), "av170001")
			if err != nil || got.Title != tt.want {
				t.Errorf("Fetch() = (%q, %v), want %q", got.Title, err, tt.want)
			}
		})
	}
}

// TestFetchSegmentCount 段数为 ceil(时长 / 360)，时长为 0 时也取第 1 段。
func TestFetchSegmentCount(t *testing.T) {
	for _, tt := range []struct{ duration, segments int }{{0, 1}, {1, 1}, {360, 1}, {361, 2}, {1451, 5}} {
		samples := map[string][]response{
			testView:  {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: tt.duration})},
			"xml-101": {emptyXML(t)},
		}
		for n := 1; n <= 6; n++ {
			samples[fmt.Sprintf("seg-101-%d", n)] = []response{segResponse()}
		}
		fake := newFake(t, samples)
		if _, err := fetch(t, fake.adapter(), "av170001"); err != nil {
			t.Fatal(err)
		}
		if got := countPrefix(fake.requested(), "seg-"); got != tt.segments {
			t.Errorf("时长 %d 秒：请求了 %d 段，want %d", tt.duration, got, tt.segments)
		}
	}
}

func TestFetchPageOutOfRange(t *testing.T) {
	fake := newFake(t, map[string][]response{
		testView: {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 10}, viewPage{Page: 2, CID: 102, Duration: 10})},
	})

	_, err := fetch(t, fake.adapter(), "av170001?p=3")

	assertError(t, err, source.NotFound)
	if got := fake.requested(); !slices.Equal(got, []string{testView}) {
		t.Errorf("请求 = %q, want 只取元数据", got)
	}
}

// TestFetchDanmakuClosed 弹幕已关闭：seg.so 只返回 state=1，XML 的 state 为 1、没有弹幕，拉取成功、0 条。
func TestFetchDanmakuClosed(t *testing.T) {
	closedXML := `<?xml version="1.0" encoding="UTF-8"?><i><chatserver>chat.bilibili.com</chatserver><chatid>101</chatid>` +
		`<mission>0</mission><maxlimit>1500</maxlimit><state>1</state><real_name>0</real_name></i>`
	fake := newFake(t, map[string][]response{
		testView:    {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 30})},
		"seg-101-1": {{status: http.StatusOK, contentType: "application/octet-stream", body: []byte{0x10, 0x01}}},
		"xml-101":   {xmlResponse(t, []byte(closedXML))},
	})

	got, err := fetch(t, fake.adapter(), "av170001")

	if err != nil || len(got.Danmaku) != 0 || got.Title != "视频" || got.Duration != 30 {
		t.Errorf("Fetch() = (%+v, %v), want 成功、0 条", got, err)
	}
}

// TestFetchErrors 错误归类：每个用例里出错的请求一直返回同一个响应，其余请求成功。
// RateLimited 与 Upstream 重试 3 次后才失败（共 4 次请求），其余不重试。
func TestFetchErrors(t *testing.T) {
	okView := viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 10})
	// oversized 比上限多出一条弹幕的一段，前 maxBodySize 字节恰好是一个未知字段加 1000 条完整的弹幕：
	// 如果按上限截断，就会解出部分弹幕
	rec := protowire.AppendTag(nil, replyElems, protowire.BytesType)
	rec = protowire.AppendBytes(rec, elem{id: 1, mode: 1, content: "弹幕"}.encode())
	oversized := protowire.AppendTag(nil, 4, protowire.BytesType)
	oversized = protowire.AppendBytes(oversized, make([]byte, maxBodySize-1000*len(rec)-1-4)) // 长度的 varint 占 4 字节
	for range 1001 {
		oversized = append(oversized, rec...)
	}
	if len(oversized) != maxBodySize+len(rec) {
		t.Fatalf("oversized 的长度 = %d", len(oversized))
	}
	// truncatedXML 压缩流被截断；cutXML 压缩流完整，文档却没有结束
	doc := xmlDoc(`<d p="1.5,1,25,0,1700000000,0,abcdef12,1,10">弹幕</d>`)
	truncatedXML := xmlResponse(t, doc)
	truncatedXML.body = truncatedXML.body[:len(truncatedXML.body)-4]
	cutXML := xmlResponse(t, bytes.TrimSuffix(doc, []byte("</i>")))
	tests := []struct {
		name     string
		view     response
		seg      response // view 成功时第 1 段的响应
		xml      response // view 成功时 XML 的响应
		kind     source.Kind
		attempts int // 出错的那个请求被请求的次数
	}{
		{name: "view -404 不存在或已删除", view: codeResponse(-404), kind: source.NotFound, attempts: 1},
		{name: "view 62002 稿件不可见", view: codeResponse(62002), kind: source.NotFound, attempts: 1},
		{name: "view 62012 仅 UP 主自己可见", view: codeResponse(62012), kind: source.NotFound, attempts: 1},
		{name: "view -403 权限不足", view: codeResponse(-403), kind: source.AuthRequired, attempts: 1},
		{name: "view -101 未登录", view: codeResponse(-101), kind: source.AuthRequired, attempts: 1},
		{name: "view HTTP 412", view: statusResponse(http.StatusPreconditionFailed), kind: source.RateLimited, attempts: 4},
		{name: "view -412", view: codeResponse(-412), kind: source.RateLimited, attempts: 4},
		{name: "view -352", view: codeResponse(-352), kind: source.RateLimited, attempts: 4},
		{name: "view -401", view: codeResponse(-401), kind: source.RateLimited, attempts: 4},
		{name: "view -509", view: codeResponse(-509), kind: source.RateLimited, attempts: 4},
		{name: "view -799", view: codeResponse(-799), kind: source.RateLimited, attempts: 4},
		{name: "view v_voucher", view: jsonResponse(`{"code":0,"message":"0","data":{"v_voucher":"voucher_123"}}`), kind: source.RateLimited, attempts: 4},
		{name: "view 62004 审核中", view: codeResponse(62004), kind: source.Upstream, attempts: 4},
		{name: "view 未知错误码", view: codeResponse(-500), kind: source.Upstream, attempts: 4},
		{name: "view HTTP 500", view: statusResponse(http.StatusInternalServerError), kind: source.Upstream, attempts: 4},
		{name: "view HTTP 503", view: statusResponse(http.StatusServiceUnavailable), kind: source.Upstream, attempts: 4},
		{name: "view HTTP 404", view: statusResponse(http.StatusNotFound), kind: source.Upstream, attempts: 4},
		{name: "view HTTP 304", view: response{status: http.StatusNotModified}, kind: source.Upstream, attempts: 4},
		{name: "view 跳转：不跟随", view: redirectResponse("https://www.bilibili.com/video/av170001"), kind: source.Upstream, attempts: 4},
		{name: "view 不是 JSON", view: response{status: http.StatusOK, contentType: "text/html", body: []byte("<html></html>")}, kind: source.Upstream, attempts: 4},
		{name: "view data 结构不对", view: jsonResponse(`{"code":0,"data":{"pages":"x"}}`), kind: source.Upstream, attempts: 4},
		{name: "view 分 P 没有 cid：响应能解析、内容不对，不重试", view: viewResponse(t, "视频", viewPage{Page: 1, Duration: 10}), kind: source.Upstream, attempts: 1},
		{name: "seg.so HTTP 412", view: okView, seg: statusResponse(http.StatusPreconditionFailed), kind: source.RateLimited, attempts: 4},
		{name: "seg.so -352", view: okView, seg: codeResponse(-352), kind: source.RateLimited, attempts: 4},
		{name: "seg.so -404：不能据此判断弹幕源不存在", view: okView, seg: codeResponse(-404), kind: source.Upstream, attempts: 4},
		{name: "seg.so -101", view: okView, seg: codeResponse(-101), kind: source.AuthRequired, attempts: 1},
		{name: "seg.so 返回 code 为 0 的 JSON", view: okView, seg: jsonResponse(`{"code":0}`), kind: source.Upstream, attempts: 4},
		{name: "seg.so HTTP 502", view: okView, seg: statusResponse(http.StatusBadGateway), kind: source.Upstream, attempts: 4},
		{
			name: "seg.so protobuf 截断", view: okView, kind: source.Upstream, attempts: 4,
			seg: response{status: http.StatusOK, contentType: "application/octet-stream", body: []byte{0x0A, 0x10, 0x08}},
		},
		{
			name: "seg.so 响应体超过上限：不截断", view: okView, kind: source.Upstream, attempts: 4,
			seg: response{status: http.StatusOK, contentType: "application/octet-stream", body: oversized},
		},
		{name: "XML HTTP 412", view: okView, xml: statusResponse(http.StatusPreconditionFailed), kind: source.RateLimited, attempts: 4},
		{name: "XML -352", view: okView, xml: codeResponse(-352), kind: source.RateLimited, attempts: 4},
		{name: "XML -404：不能据此判断弹幕源不存在", view: okView, xml: codeResponse(-404), kind: source.Upstream, attempts: 4},
		{name: "XML -101", view: okView, xml: codeResponse(-101), kind: source.AuthRequired, attempts: 1},
		{name: "XML HTTP 502", view: okView, xml: statusResponse(http.StatusBadGateway), kind: source.Upstream, attempts: 4},
		{name: "XML HTTP 304", view: okView, xml: response{status: http.StatusNotModified}, kind: source.Upstream, attempts: 4},
		{name: "XML 压缩流截断", view: okView, xml: truncatedXML, kind: source.Upstream, attempts: 4},
		{name: "XML 文档没有结束：不解出部分弹幕", view: okView, xml: cutXML, kind: source.Upstream, attempts: 4},
		{
			name: "XML 不认识的压缩方式", view: okView, kind: source.Upstream, attempts: 4,
			xml: response{status: http.StatusOK, contentType: "text/xml", contentEncoding: "br", body: []byte("x")},
		},
		{name: "XML 不是 XML", view: okView, xml: statusResponse(http.StatusOK), kind: source.Upstream, attempts: 4},
		{
			name: "XML 有 <d> 却一条都认不出：格式变了，不悄悄变成 0 条", view: okView, kind: source.Upstream, attempts: 4,
			xml: xmlResponse(t, xmlDoc(`<d p='1.5,1,25,0,1700000000,0,abcdef12,1,10'>单引号</d>`)),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			samples := map[string][]response{testView: {tt.view}, "seg-101-1": {segResponse()}, "xml-101": {emptyXML(t)}}
			failing := testView
			if tt.seg.status != 0 {
				samples["seg-101-1"] = []response{tt.seg}
				failing = "seg-101-1"
			}
			if tt.xml.status != 0 {
				samples["xml-101"] = []response{tt.xml}
				failing = "xml-101"
			}
			fake := newFake(t, samples)

			got, err := fetch(t, fake.adapter(), "av170001")

			assertError(t, err, tt.kind)
			if !reflect.DeepEqual(got, source.Fetched{}) {
				t.Errorf("出错时应返回零值，实际 %+v", got)
			}
			if n := countOf(fake.requested(), failing); n != tt.attempts {
				t.Errorf("%s 被请求 %d 次，want %d", failing, n, tt.attempts)
			}
		})
	}
}

func countOf(requests []string, name string) int {
	n := 0
	for _, r := range requests {
		if r == name {
			n++
		}
	}
	return n
}

// countPrefix 样本名以 prefix 开头的请求数。
func countPrefix(requests []string, prefix string) int {
	n := 0
	for _, r := range requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// TestFetchRetrySucceeds rate_limited、upstream 在重试次数以内恢复时照常成功。
func TestFetchRetrySucceeds(t *testing.T) {
	fake := newFake(t, map[string][]response{
		testView: {
			statusResponse(http.StatusPreconditionFailed),
			codeResponse(-799),
			viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 10}),
		},
		"seg-101-1": {
			statusResponse(http.StatusBadGateway),
			{status: http.StatusOK, contentType: "application/octet-stream", body: []byte{0x0A}}, // 截断
			statusResponse(http.StatusPreconditionFailed),
			segResponse(elem{id: 1, mode: 1, content: "终于"}.encode()),
		},
		"xml-101": {
			statusResponse(http.StatusBadGateway),
			xmlResponse(t, xmlDoc(`<d p="1.5,1,25,0,1700000000,0,abcdef12,2,10">也终于</d>`)),
		},
	})

	got, err := fetch(t, fake.adapter(), "av170001")

	if err != nil || len(got.Danmaku) != 2 {
		t.Fatalf("Fetch() = (%+v, %v), want 2 条", got, err)
	}
	if n := len(fake.requested()); n != 3+4+2 {
		t.Errorf("共请求 %d 次，want 9", n)
	}
}

// TestFetchAllOrNothing 有一段最终失败时整体失败，不返回其他段和 XML 的弹幕。
func TestFetchAllOrNothing(t *testing.T) {
	samples := map[string][]response{
		testView:  {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 1800})},
		"xml-101": {xmlResponse(t, xmlDoc(`<d p="1.5,1,25,0,1700000000,0,abcdef12,9,10">XML</d>`))},
	}
	for _, n := range []string{"1", "2", "3", "5"} {
		samples["seg-101-"+n] = []response{segResponse(elem{id: 1, mode: 1, content: "第 " + n + " 段"}.encode())}
	}
	samples["seg-101-4"] = []response{statusResponse(http.StatusInternalServerError)}
	fake := newFake(t, samples)

	got, err := fetch(t, fake.adapter(), "av170001")

	assertError(t, err, source.Upstream)
	if !reflect.DeepEqual(got, source.Fetched{}) {
		t.Errorf("出错时应返回零值，实际 %+v", got)
	}
}

// TestFetchSegmentConcurrency 单个绑定内的分段与 XML 并发请求，同时进行的请求数恰好达到上限 3。
func TestFetchSegmentConcurrency(t *testing.T) {
	samples := map[string][]response{
		testView:  {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 3600})},
		"xml-101": {emptyXML(t)},
	}
	for n := 1; n <= 10; n++ {
		samples[fmt.Sprintf("seg-101-%d", n)] = []response{segResponse()}
	}
	fake := newFake(t, samples)
	fake.hold = 20 * time.Millisecond

	if _, err := fetch(t, fake.adapter(), "av170001"); err != nil {
		t.Fatal(err)
	}

	if len(fake.requested()) != 12 {
		t.Errorf("请求 = %q, want 元数据、XML 加 10 段", fake.requested())
	}
	if fake.maxInflight != 3 {
		t.Errorf("最多同时请求了 %d 个，want 3", fake.maxInflight)
	}
}

// TestFetchTimeout 调用方的 ctx 超时按 Upstream 处理，不再重试。
func TestFetchTimeout(t *testing.T) {
	fake := startFake(t, func(_ string, r *http.Request) (response, bool) {
		<-r.Context().Done() // 一直不响应
		return statusResponse(http.StatusGatewayTimeout), true
	})
	ref, err := fake.adapter().ParseLink(t.Context(), "av170001")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err = fake.adapter().Fetch(ctx, ref)

	assertError(t, err, source.Upstream)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want 底层原因为 context.DeadlineExceeded", err)
	}
	if n := len(fake.requested()); n != 1 {
		t.Errorf("超时后不应重试，共请求 %d 次", n)
	}
}

// TestSessdata 配置了 SESSDATA 时只作为 Cookie 发给 bilibili.com 的子域名（view、seg.so、XML），短链不带；XML 照样合并。
// Cookie 的检查在假 B 站的 checkRequest 里，对每个用例都生效：没配置时任何请求都不带 Cookie。
func TestSessdata(t *testing.T) {
	fake := newFake(t, map[string][]response{
		"short-b23.tv-abcdefg": {redirectResponse("https://www.bilibili.com/video/av170001")},
		testView:               {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 10})},
		"seg-101-1":            {segResponse(elem{id: 1, mode: 1, content: "protobuf"}.encode())},
		"xml-101":              {xmlResponse(t, xmlDoc(`<d p="1.5,1,25,0,1700000000,0,abcdef12,2,10">XML</d>`))},
	})
	fake.sessdata = "1a2b3c4d%2C1790000000%2Cabcde*a1"

	got, err := fetch(t, fake.adapter(), "https://b23.tv/abcdefg")

	if err != nil || len(got.Danmaku) != 2 {
		t.Fatalf("Fetch() = (%+v, %v), want protobuf 与 XML 各 1 条", got, err)
	}
	want := []string{"seg-101-1", "short-b23.tv-abcdefg", testView, "xml-101"}
	if got := slices.Sorted(slices.Values(fake.requested())); !slices.Equal(got, want) {
		t.Errorf("请求 = %q, want %q", got, want)
	}
}

func TestAdapterIdentity(t *testing.T) {
	a := New(config.Bilibili{})
	if a.ID() != "bilibili" || a.Platform() != danmaku.PlatformBilibili {
		t.Errorf("ID() = %q, Platform() = %q", a.ID(), a.Platform())
	}
}
