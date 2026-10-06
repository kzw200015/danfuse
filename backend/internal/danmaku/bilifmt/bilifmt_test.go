package bilifmt

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// doc 一份完整的 XML 弹幕，头部与 B 站返回的相同。
func doc(ds ...string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><i><chatserver>chat.bilibili.com</chatserver><chatid>101</chatid>` +
		`<mission>0</mission><maxlimit>1000</maxlimit><state>0</state><real_name>0</real_name><source>k-v</source>` +
		strings.Join(ds, "") + `</i>`)
}

// TestDecode XML 弹幕的解析与字段映射：p 套用与 protobuf 相同的映射，格式不对的单条丢弃。每个用例一份 XML。
func TestDecode(t *testing.T) {
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
			got, err := Decode(doc(tt.ds...))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Decode() = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// TestDecodeErrors 文档不完整、或有 <d> 却一条都认不出时整份报错；没有弹幕的文档解出 0 条。
func TestDecodeErrors(t *testing.T) {
	tests := []struct {
		name    string
		doc     []byte
		wantErr bool
	}{
		{"没有弹幕", doc(), false},
		{"带 BOM、结尾有空白", append([]byte("\ufeff"), append(doc(`<d p="1,1,25,0,0,0,x,7,10">a</d>`), "\r\n"...)...), false},
		{"</i> 之后附了注释", append(doc(`<d p="1,1,25,0,0,0,x,7,10">a</d>`), "\n<!--凑够1KB-->\n\n<!--好像还不够啊-->\n"...), false},
		{"没有以 </i> 结尾：被截断", doc(`<d p="1,1,25,0,0,0,x,7,10">a</d>`)[:80], true},
		{"有 <d> 却一条都认不出", doc(`<d p='1,1,25,0,0,0,x,7,10'>单引号</d>`), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Decode(tt.doc); (err != nil) != tt.wantErr {
				t.Errorf("Decode() err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
