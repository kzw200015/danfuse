package danmakufile

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// xmlFile 一份 B 站导出的 XML 弹幕文件，头部与旧版导出的相同。
func xmlFile(ds ...string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><i><chatserver>chat.bilibili.com</chatserver><chatid>934042</chatid>` +
		`<mission>0</mission><maxlimit>3000</maxlimit><source>k-v</source>` + strings.Join(ds, "\n") + `</i>`)
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    []danmaku.Danmaku
		wantErr bool
	}{
		{
			name: "B 站 XML：原始 ID 取 dmid",
			data: xmlFile(
				`<d p="221.719,1,25,16777215,1373250214,0,d9df08a7,251930361">这声音。。。</d>`,
				`<d p="385.734,5,25,16711680,1373250271,0,3ae9ab45,251930634">噗—</d>`,
			),
			want: []danmaku.Danmaku{
				{SourceID: 251930361, TimeMs: 221719, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "这声音。。。"},
				{SourceID: 251930634, TimeMs: 385734, Mode: danmaku.ModeTop, Color: 0xFF0000, Text: "噗—"},
			},
		},
		{
			name: "带 BOM",
			data: append([]byte("\xef\xbb\xbf"), xmlFile(`<d p="1.5,1,25,0,0,0,x,7">a</d>`)...),
			want: []danmaku.Danmaku{{SourceID: 7, TimeMs: 1500, Mode: danmaku.ModeScroll, Text: "a"}},
		},
		{
			name: "</i> 之后附了注释",
			data: append(xmlFile(`<d p="1.5,1,25,0,0,0,x,7">a</d>`), "\n<!--凑够1KB-->\n\n<!--好像还不够啊-->\n"...),
			want: []danmaku.Danmaku{{SourceID: 7, TimeMs: 1500, Mode: danmaku.ModeScroll, Text: "a"}},
		},
		{name: "没有弹幕", data: xmlFile(), want: nil},
		{name: "少了最后的 >", data: bytes.TrimSuffix(xmlFile(`<d p="1.5,1,25,0,0,0,x,7">a</d>`), []byte(">")), wantErr: true},
		{name: "被截断", data: xmlFile(`<d p="1.5,1,25,0,0,0,x,7">a</d>`)[:120], wantErr: true},
		{name: "HTML", data: []byte("<html><body>来自新世界的B站历史弹幕</body></html>"), wantErr: true},
		{name: "JSON", data: []byte(`[{"c":"0,16777215,1,25,4d0568ac,1389087514","m":"QAQ"}]`), wantErr: true},
		{name: "不是 B 站的 XML", data: []byte(`<?xml version="1.0"?><root><item>x</item></root>`), wantErr: true},
		{name: "空文件", data: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse("1.xml", tt.data)
			if tt.wantErr {
				fileErr, ok := errors.AsType[*Error](err)
				if !ok || !strings.Contains(fileErr.Message, "「1.xml」") {
					t.Errorf("Parse() err = %v, want *Error 提到文件名", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse() = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestTitle(t *testing.T) {
	tests := []struct{ name, want string }{
		{"20130709.xml", "20130709"},
		{"1-「最棒的也是最烂的律师，爱和法律都说谎？！」.xml", "1-「最棒的也是最烂的律师，爱和法律都说谎？！」"},
		{"av339145-  01.xml", "av339145-  01"},
		{"没有扩展名", "没有扩展名"},
		{".xml", ".xml"},
	}
	for _, tt := range tests {
		if got := Title(tt.name); got != tt.want {
			t.Errorf("Title(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
