package bilibili

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

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
	srcErr, ok := errors.AsType[*source.Error](err)
	if !ok {
		t.Fatalf("error = %v, want *source.Error", err)
	}
	wantMessage := map[source.Kind]string{
		source.NotFound:     "视频不存在、已删除或不可见",
		source.AuthRequired: "需要登录，请配置 SESSDATA",
		source.RateLimited:  "B 站限流，请稍后再试",
		source.Upstream:     "B 站接口异常",
	}[kind]
	if srcErr.Kind != kind || srcErr.Message != wantMessage {
		t.Errorf("error = {Kind: %d, Message: %q}, want {Kind: %d, Message: %q}（%v）", srcErr.Kind, srcErr.Message, kind, wantMessage, err)
	}
}

func TestParseLink(t *testing.T) {
	const p1, p2, p3 = `{"kind":"video","aid":170001,"page":1}`, `{"kind":"video","aid":170001,"page":2}`, `{"kind":"video","aid":170001,"page":3}`
	tests := []struct {
		link string
		want string // 为空表示无法识别
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
		{"https://www.bilibili.com/bangumi/play/ep508404", ""},
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

			if tt.want == "" {
				srcErr, ok := errors.AsType[*source.Error](err)
				if !ok || srcErr.Kind != source.InvalidLink || srcErr.Message != "无法识别的链接" {
					t.Errorf("ParseLink() = (%s, %v), want InvalidLink「无法识别的链接」", ref, err)
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
	a := New()
	tests := []struct {
		ref  string
		want source.Display
	}{
		{`{"kind":"video","aid":170001,"page":1}`, source.Display{URL: "https://www.bilibili.com/video/BV17x411w7KC", Label: "B 站投稿 BV17x411w7KC"}},
		{`{"kind":"video","aid":170001,"page":3}`, source.Display{URL: "https://www.bilibili.com/video/BV17x411w7KC?p=3", Label: "B 站投稿 BV17x411w7KC P3"}},
		{`{"aid":641107054,"page":12,"kind":"video"}`, source.Display{URL: "https://www.bilibili.com/video/BV1ZY4y187fA?p=12", Label: "B 站投稿 BV1ZY4y187fA P12"}},
		{`{"kind":"video","aid":294,"page":1}`, source.Display{URL: "https://www.bilibili.com/video/BV1xx411c7XX", Label: "B 站投稿 BV1xx411c7XX"}},
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
	a := New()
	for _, link := range []string{"av170001", "av170001?p=7", "BV1ZY4y187fA", "av1", "av2251799813685247"} {
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
		`{"kind":"episode","epId":508404}`,
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
		LogAttrs: []slog.Attr{slog.Int64("cid", 102), slog.Int("protobuf", 3)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Fetch() = %+v\nwant %+v", got, want)
	}
	if req := fake.requested(); len(req) != 4 || req[0] != testView {
		t.Errorf("请求 = %q, want 先取元数据，再取 3 段", req)
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
		samples := map[string][]response{testView: {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: tt.duration})}}
		for n := 1; n <= 6; n++ {
			samples[fmt.Sprintf("seg-101-%d", n)] = []response{segResponse()}
		}
		fake := newFake(t, samples)
		if _, err := fetch(t, fake.adapter(), "av170001"); err != nil {
			t.Fatal(err)
		}
		if got := len(fake.requested()) - 1; got != tt.segments {
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

// TestFetchDanmakuClosed 弹幕已关闭：seg.so 只返回 state=1，拉取成功、0 条。
func TestFetchDanmakuClosed(t *testing.T) {
	fake := newFake(t, map[string][]response{
		testView:    {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 30})},
		"seg-101-1": {{status: http.StatusOK, contentType: "application/octet-stream", body: []byte{0x10, 0x01}}},
	})

	got, err := fetch(t, fake.adapter(), "av170001")

	if err != nil || len(got.Danmaku) != 0 || got.Title != "视频" || got.Duration != 30 {
		t.Errorf("Fetch() = (%+v, %v), want 成功、0 条", got, err)
	}
}

// TestDecodeSegment 字段映射与未知字段的跳过。每个用例一段，里面是构造的 DanmakuElem。
func TestDecodeSegment(t *testing.T) {
	text := func(s string) elem { return elem{id: 1, progress: 1500, mode: 1, color: 0x123456, content: s} }
	want := func(s string) []danmaku.Danmaku {
		return []danmaku.Danmaku{{SourceID: 1, TimeMs: 1500, Mode: danmaku.ModeScroll, Color: 0x123456, Text: s}}
	}
	// unknown 实测出现过的 proto 未定义字段（15/20/26 等）和各种类型的未知字段
	var unknown []byte
	unknown = protowire.AppendTag(unknown, 15, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 3)
	unknown = protowire.AppendTag(unknown, 20, protowire.BytesType)
	unknown = protowire.AppendString(unknown, "0")
	unknown = protowire.AppendTag(unknown, 26, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 279786)
	unknown = protowire.AppendTag(unknown, 99, protowire.Fixed64Type)
	unknown = protowire.AppendFixed64(unknown, 7)
	unknown = protowire.AppendTag(unknown, 98, protowire.Fixed32Type)
	unknown = protowire.AppendFixed32(unknown, 7)
	unknown = protowire.AppendTag(unknown, 97, protowire.StartGroupType)
	unknown = protowire.AppendTag(unknown, 1, protowire.VarintType)
	unknown = protowire.AppendVarint(unknown, 1)
	unknown = protowire.AppendTag(unknown, 97, protowire.EndGroupType)
	// known 已知的其他字段：fontsize、midHash、ctime、weight、pool、idStr、attr
	var known []byte
	for _, f := range []struct {
		num protowire.Number
		v   any
	}{{4, 25}, {6, "abcdef12"}, {8, 1700000000}, {9, 10}, {11, 1}, {12, "1"}, {13, 4}} {
		switch v := f.v.(type) {
		case int:
			known = protowire.AppendTag(known, f.num, protowire.VarintType)
			known = protowire.AppendVarint(known, uint64(v))
		case string:
			known = protowire.AppendTag(known, f.num, protowire.BytesType)
			known = protowire.AppendString(known, v)
		}
	}
	// wrongType 已知字段号却是别的类型，当作未知字段跳过
	var wrongType []byte
	wrongType = protowire.AppendTag(wrongType, elemColor, protowire.BytesType)
	wrongType = protowire.AppendString(wrongType, "red")
	wrongType = protowire.AppendTag(wrongType, elemContent, protowire.VarintType)
	wrongType = protowire.AppendVarint(wrongType, 1)

	tests := []struct {
		name  string
		elems []elem
		want  []danmaku.Danmaku
	}{
		{
			name: "模式 1、2、3 合并为滚动，4、5、6 原样保留",
			elems: []elem{
				{id: 1, mode: 1, content: "a"},
				{id: 2, mode: 2, content: "b"},
				{id: 3, mode: 3, content: "c"},
				{id: 4, mode: 4, content: "d"},
				{id: 5, mode: 5, content: "e"},
				{id: 6, mode: 6, content: "f"},
			},
			want: []danmaku.Danmaku{
				{SourceID: 1, Mode: 1, Text: "a"},
				{SourceID: 2, Mode: 1, Text: "b"},
				{SourceID: 3, Mode: 1, Text: "c"},
				{SourceID: 4, Mode: 4, Text: "d"},
				{SourceID: 5, Mode: 5, Text: "e"},
				{SourceID: 6, Mode: 6, Text: "f"},
			},
		},
		{
			name: "模式 7、8、9 和其他取值丢弃",
			elems: []elem{
				{id: 7, mode: 7, content: "高级"},
				{id: 8, mode: 8, content: "代码"},
				{id: 9, mode: 9, content: "BAS"},
				{id: 10, content: "没有模式"},
				{id: 11, mode: 10, content: "未知模式"},
				{id: 12, mode: -1, content: "负数"},
			},
		},
		{
			name:  "颜色只取低 24 位，缺省为 0",
			elems: []elem{{id: 1, mode: 1, color: 0xFFFFFFFF, content: "a"}, {id: 2, mode: 1, color: 0x12345678, content: "b"}, {id: 3, mode: 1, content: "c"}},
			want:  []danmaku.Danmaku{{SourceID: 1, Mode: 1, Color: 0xFFFFFF, Text: "a"}, {SourceID: 2, Mode: 1, Color: 0x345678, Text: "b"}, {SourceID: 3, Mode: 1, Text: "c"}},
		},
		{
			name:  "时间缺省为 0，原始 ID 超过 2^53 时原样保留",
			elems: []elem{{id: 2213969828606745088, mode: 1, content: "a"}},
			want:  []danmaku.Danmaku{{SourceID: 2213969828606745088, Mode: 1, Text: "a"}},
		},
		{
			name:  "没有 ID 的丢弃",
			elems: []elem{{mode: 1, content: "a"}},
		},
		{
			name:  "非法的 UTF-8 字节被清洗",
			elems: []elem{text("ab\xffc\xe4\xb8d")},
			want:  want("abcd"),
		},
		{
			name:  "NUL 被去掉",
			elems: []elem{text("a\x00b")},
			want:  want("ab"),
		},
		{
			name:  "去掉空白后为空的丢弃",
			elems: []elem{text(" \t\n　"), text("\xff\xfe"), {id: 2, mode: 1}},
		},
		{
			name:  "其余原样保留：首尾空白、emoji、&<>、换行",
			elems: []elem{text(" 前后有空格 "), text("😀🎉"), text(`&<>"'`), text("第一行\n第二行")},
			want: slices.Concat(
				want(" 前后有空格 "), want("😀🎉"), want(`&<>"'`), want("第一行\n第二行"),
			),
		},
		{
			name:  "未知字段与用不到的已知字段被跳过",
			elems: []elem{{id: 1, progress: 1500, mode: 1, color: 0x123456, content: "a", extra: slices.Concat(known, unknown, wrongType)}},
			want:  want("a"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var elems [][]byte
			for _, e := range tt.elems {
				elems = append(elems, e.encode())
			}
			seg := segResponse(elems...)
			// 顶层的未知字段：实测出现的字段 4（bytes）、5（colorfulSrc），以及 state
			seg.body = protowire.AppendTag(seg.body, 4, protowire.BytesType)
			seg.body = protowire.AppendBytes(seg.body, []byte{0x08, 0x01})
			seg.body = protowire.AppendTag(seg.body, 2, protowire.VarintType)
			seg.body = protowire.AppendVarint(seg.body, 0)
			seg.body = protowire.AppendTag(seg.body, 5, protowire.BytesType)
			seg.body = protowire.AppendString(seg.body, "\x08\x01\x12\x02{}")

			fake := newFake(t, map[string][]response{
				testView:    {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 10})},
				"seg-101-1": {seg},
			})
			got, err := fetch(t, fake.adapter(), "av170001")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Danmaku, tt.want) {
				t.Errorf("Danmaku = %+v\nwant %+v", got.Danmaku, tt.want)
			}
		})
	}
}

// TestFetchErrors 错误归类：每个用例里出错的请求一直返回同一个响应。
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
	tests := []struct {
		name     string
		view     response
		seg      response // view 成功时第 1 段的响应
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			samples := map[string][]response{testView: {tt.view}}
			failing := testView
			if tt.seg.status != 0 {
				samples["seg-101-1"] = []response{tt.seg}
				failing = "seg-101-1"
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
	})

	got, err := fetch(t, fake.adapter(), "av170001")

	if err != nil || len(got.Danmaku) != 1 {
		t.Fatalf("Fetch() = (%+v, %v), want 1 条", got, err)
	}
	if n := len(fake.requested()); n != 3+4 {
		t.Errorf("共请求 %d 次，want 7", n)
	}
}

// TestFetchAllOrNothing 有一段最终失败时整体失败，不返回其他段的弹幕。
func TestFetchAllOrNothing(t *testing.T) {
	samples := map[string][]response{testView: {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 1800})}}
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

// TestFetchSegmentConcurrency 单个绑定内的分段并发请求，同时请求的段数恰好达到上限 3。
func TestFetchSegmentConcurrency(t *testing.T) {
	samples := map[string][]response{testView: {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 3600})}}
	for n := 1; n <= 10; n++ {
		samples[fmt.Sprintf("seg-101-%d", n)] = []response{segResponse()}
	}
	fake := newFake(t, samples)
	fake.hold = 20 * time.Millisecond

	if _, err := fetch(t, fake.adapter(), "av170001"); err != nil {
		t.Fatal(err)
	}

	if len(fake.requested()) != 11 {
		t.Errorf("请求 = %q, want 元数据加 10 段", fake.requested())
	}
	if fake.maxInflight != 3 {
		t.Errorf("最多同时请求了 %d 段，want 3", fake.maxInflight)
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

func TestAdapterIdentity(t *testing.T) {
	a := New()
	if a.ID() != "bilibili" || a.Platform() != danmaku.PlatformBilibili {
		t.Errorf("ID() = %q, Platform() = %q", a.ID(), a.Platform())
	}
}
