package bilibili

import (
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// TestDecodeXML XML 弹幕的解析与字段映射：p 套用与 protobuf 相同的映射，格式不对的单条丢弃。每个用例一份 XML。
func TestDecodeXML(t *testing.T) {
	// d 一条 <d>：p 的 9 项依次为时间（秒）、模式、字号、颜色、发送时间、弹幕池、发送者哈希、原始 ID、屏蔽等级
	d := func(p, text string) string { return `<d p="` + p + `">` + text + `</d>` }
	want := func(text string) []danmaku.Danmaku {
		return []danmaku.Danmaku{{SourceID: 7, TimeMs: 1500, Mode: danmaku.ModeScroll, Color: 0x123456, Text: text}}
	}
	text := func(s string) string { return d("1.50000,1,25,1193046,1700000000,0,abcdef12,7,10", s) }
	tests := []struct {
		name string
		ds   []string
		want []danmaku.Danmaku
	}{
		{
			name: "时间从秒换算成毫秒并四舍五入，第 8 项为原始 ID，超过 2^53 时原样保留",
			ds: []string{
				d("0.00000,1,25,16777215,1700000000,0,abcdef12,1,10", "开头"),
				d("95.23400,4,25,0,1700000000,0,abcdef12,38749444289069063,10", "底部"),
				d("12.3456,5,25,65280,1700000000,0,abcdef12,2213969828606745088,5", "顶部"),
			},
			want: []danmaku.Danmaku{
				{SourceID: 1, TimeMs: 0, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "开头"},
				{SourceID: 38749444289069063, TimeMs: 95234, Mode: danmaku.ModeBottom, Color: 0, Text: "底部"},
				{SourceID: 2213969828606745088, TimeMs: 12346, Mode: danmaku.ModeTop, Color: 0x00FF00, Text: "顶部"},
			},
		},
		{
			name: "模式 1、2、3 合并为滚动，6 原样保留，7、8、9 和其他取值丢弃",
			ds: []string{
				d("1,2,25,0,0,0,x,1,10", "a"), d("1,3,25,0,0,0,x,2,10", "b"), d("1,6,25,0,0,0,x,3,10", "c"),
				d("1,7,25,0,0,0,x,4,10", "高级"), d("1,8,25,0,0,0,x,5,10", "代码"), d("1,9,25,0,0,0,x,6,10", "BAS"),
				d("1,0,25,0,0,0,x,7,10", "零"), d("1,-1,25,0,0,0,x,8,10", "负数"),
			},
			want: []danmaku.Danmaku{
				{SourceID: 1, TimeMs: 1000, Mode: danmaku.ModeScroll, Text: "a"},
				{SourceID: 2, TimeMs: 1000, Mode: danmaku.ModeScroll, Text: "b"},
				{SourceID: 3, TimeMs: 1000, Mode: danmaku.ModeReverse, Text: "c"},
			},
		},
		{
			name: "颜色只取低 24 位",
			ds:   []string{d("1.5,1,25,4294967295,0,0,x,7,10", "a")},
			want: []danmaku.Danmaku{{SourceID: 7, TimeMs: 1500, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "a"}},
		},
		{
			name: "特殊正文：emoji、实体转义、字符引用、换行",
			ds: []string{
				text("😀🎉"),
				text("&amp;&lt;&gt;&quot;&apos;"),
				text("&#34;&#39;&#x26;"),
				text("第一行\n第二行"),
				text("第一行&#xA;第二行"),
			},
			want: slices.Concat(want("😀🎉"), want(`&<>"'`), want(`"'&`), want("第一行\n第二行"), want("第一行\n第二行")),
		},
		{
			name: "XML 不允许的字符也能解析：非法 UTF-8 字节被清洗，控制字符原样保留，NUL 被去掉",
			ds:   []string{text("a\xffb"), text("a\x08b"), text("a&#8;b"), text("a&#0;b\x00c")},
			want: slices.Concat(want("ab"), want("a\x08b"), want("a\x08b"), want("a�bc")),
		},
		{
			name: "格式不对的单条丢弃，不影响其他弹幕",
			ds: []string{
				d("1.5,1,25,1193046,1700000000,0,abcdef12", "少一项"),
				d("abc,1,25,1193046,1700000000,0,abcdef12,7,10", "时间不是数"),
				d("NaN,1,25,1193046,1700000000,0,abcdef12,7,10", "时间是 NaN"),
				d("1e10,1,25,1193046,1700000000,0,abcdef12,7,10", "时间超出 int32"),
				d("1.5,x,25,1193046,1700000000,0,abcdef12,7,10", "模式不是数"),
				d("1.5,1,25,-1,1700000000,0,abcdef12,7,10", "颜色为负"),
				d("1.5,1,25,1193046,1700000000,0,abcdef12,abc,10", "ID 不是数"),
				d("1.5,1,25,1193046,1700000000,0,abcdef12,0,10", "ID 为 0"),
				`<d p="1.5,1,25,1193046,1700000000,0,abcdef12,7,10"/>`,
				`<d p='1.5,1,25,1193046,1700000000,0,abcdef12,7,10'>单引号认不出</d>`,
				`<d data-p="1.5,1,25,1193046,1700000000,0,abcdef12,7,10">不是 p 属性</d>`,
				text(" \t\n　"),
				text("留下"),
			},
			want: want("留下"),
		},
		{
			name: "p 之外还有别的属性，顺序不限；属性值也反转义",
			ds: []string{
				`<d id="1" p="1.50000,1,25,1193046,1700000000,0,abcdef12,7,10" user="x">p 在中间</d>`,
				`<d p="1.50000,1,25,1193046,1700000000,0,abcdef12,7,10"	pos="9">p 在前面</d>`,
				"<d\tid=\"1\"\np=\"1.50000,1,25,1193046,1700000000,0,abcdef12,7,10\">换行分隔</d>",
				`<d p="1.50000,1,25,1193046,1700000000,0,abcdef12,&#55;,10">字符引用</d>`,
			},
			want: slices.Concat(want("p 在中间"), want("p 在前面"), want("换行分隔"), want("字符引用")),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake(t, map[string][]response{
				testView:    {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 10})},
				"seg-101-1": {segResponse()},
				"xml-101":   {xmlResponse(t, xmlDoc(tt.ds...))},
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

// TestDecodeXMLUncompressed 响应没有压缩时照样解析。
func TestDecodeXMLUncompressed(t *testing.T) {
	plain := xmlResponse(t, nil)
	plain.contentEncoding, plain.body = "", xmlDoc(`<d p="1.5,1,25,0,0,0,x,7,10">a</d>`)
	fake := newFake(t, map[string][]response{
		testView:    {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 10})},
		"seg-101-1": {segResponse()},
		"xml-101":   {plain},
	})

	got, err := fetch(t, fake.adapter(), "av170001")

	if err != nil || len(got.Danmaku) != 1 {
		t.Errorf("Fetch() = (%+v, %v), want 1 条", got.Danmaku, err)
	}
}

// TestFetchMergesXML protobuf 与 XML 按原始 ID 合并：两边都有的保留 protobuf 那条，XML 独有的接在后面；
// LogAttrs 记下 protobuf 条数、XML 条数和两者的交集。
func TestFetchMergesXML(t *testing.T) {
	fake := newFake(t, map[string][]response{
		testView: {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 400})},
		"seg-101-1": {segResponse(
			elem{id: 1, progress: 1000, mode: 1, content: "只在 protobuf"}.encode(),
			elem{id: 2, progress: 2000, mode: 4, color: 0xFF0000, content: "两边都有"}.encode(),
		)},
		"seg-101-2": {segResponse(elem{id: 3, progress: 361000, mode: 1, content: "第二段"}.encode())},
		"xml-101": {xmlResponse(t, xmlDoc(
			`<d p="5.00000,1,25,16777215,1700000000,0,x,4,10">只在 XML</d>`,
			`<d p="2.00000,4,25,16711680,1700000000,0,x,2,10">两边都有（XML）</d>`,
			`<d p="361.00000,1,25,16777215,1700000000,0,x,3,10">第二段（XML）</d>`,
			`<d p="1.00000,7,25,16777215,1700000000,0,x,1,10">高级弹幕不算</d>`,
			`<d p="6.00000,5,25,255,1700000000,0,x,5,10">也只在 XML</d>`,
		))},
	})

	got, err := fetch(t, fake.adapter(), "av170001")
	if err != nil {
		t.Fatal(err)
	}
	want := []danmaku.Danmaku{
		{SourceID: 1, TimeMs: 1000, Mode: danmaku.ModeScroll, Text: "只在 protobuf"},
		{SourceID: 2, TimeMs: 2000, Mode: danmaku.ModeBottom, Color: 0xFF0000, Text: "两边都有"},
		{SourceID: 3, TimeMs: 361000, Mode: danmaku.ModeScroll, Text: "第二段"},
		{SourceID: 4, TimeMs: 5000, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "只在 XML"},
		{SourceID: 5, TimeMs: 6000, Mode: danmaku.ModeTop, Color: 0x0000FF, Text: "也只在 XML"},
	}
	if !reflect.DeepEqual(got.Danmaku, want) {
		t.Errorf("Danmaku = %+v\nwant %+v", got.Danmaku, want)
	}
	wantAttrs := []slog.Attr{slog.Int64("cid", 101), slog.Int("protobuf", 3), slog.Int("xml", 4), slog.Int("overlap", 2)}
	if !reflect.DeepEqual(got.LogAttrs, wantAttrs) {
		t.Errorf("LogAttrs = %v, want %v", got.LogAttrs, wantAttrs)
	}
}

// TestFetchXMLOnly 弹幕只在 XML 里时（例如池还没满的新视频，未登录时 protobuf 只给一部分）照样拉到。
func TestFetchXMLOnly(t *testing.T) {
	fake := newFake(t, map[string][]response{
		testView:    {viewResponse(t, "视频", viewPage{Page: 1, CID: 101, Duration: 10})},
		"seg-101-1": {{status: http.StatusNotModified}},
		"xml-101":   {xmlResponse(t, xmlDoc(`<d p="1.5,1,25,0,0,0,x,7,10">a</d>`))},
	})

	got, err := fetch(t, fake.adapter(), "av170001")

	if err != nil || len(got.Danmaku) != 1 {
		t.Errorf("Fetch() = (%+v, %v), want 1 条", got, err)
	}
}
