package bilibili

import (
	"reflect"
	"slices"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

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
				"xml-101":   {emptyXML(t)},
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
