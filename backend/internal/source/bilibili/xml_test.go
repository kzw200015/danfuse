package bilibili

import (
	"log/slog"
	"net/http"
	"reflect"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

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
