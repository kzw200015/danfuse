// Package bilifmt B 站弹幕格式与内部格式的映射：XML 弹幕的解码与编码，以及一条弹幕字段的映射（XML 与 protobuf 共用）。
// B 站源适配器拉取的 XML、protobuf，与用户上传的弹幕文件（danmakufile）都用它解码，导出用它编码。纯计算，不访问网络。
package bilifmt

import (
	"bufio"
	"bytes"
	"errors"
	"html"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// XMLElem 一条弹幕：<d p="属性">正文</d>，子匹配依次为 p 与正文（都未反转义）。p 之外还可以有别的属性，顺序不限；
// 正文里的 < 和 & 都已转义。导出给 B 站适配器的样本录制，脱敏时认出的正是解码时认得出的那些。
var XMLElem = regexp.MustCompile(`<d\s(?:[^>]*?\s)?p="([^"]*)"[^>]*>([^<]*)</d>`)

// Decode 解析 XML 弹幕并映射成内部格式，映射规则与 protobuf 相同（NewDanmaku）。
// 不用 encoding/xml：它遇到非法的 UTF-8 字节或 XML 不允许的控制字符（包括 &#8; 这样的字符引用）就整份报错，
// 一条坏弹幕会让整份失败。这里逐条匹配 <d>，属性与正文按 HTML 的规则反转义，p 的内容不对的单条丢弃。
// 要求文档完整（以 </i> 结尾，之后只能有空白和注释），被截断的文档不会只解出一部分弹幕；
// 有 <d> 却一条都认不出时说明格式变了，按错误处理，不悄悄变成 0 条。
func Decode(b []byte) ([]danmaku.Danmaku, error) {
	if !bytes.HasSuffix(trimTrailingComments(b), []byte("</i>")) {
		return nil, errors.New("not a complete danmaku XML document")
	}
	elems := XMLElem.FindAllSubmatch(b, -1)
	if len(elems) == 0 && bytes.Contains(b, []byte("<d ")) {
		return nil, errors.New("no recognizable <d> element: the XML format may have changed")
	}
	var items []danmaku.Danmaku
	for _, m := range elems {
		if d, ok := decodeElem(html.UnescapeString(string(m[1])), html.UnescapeString(string(m[2]))); ok {
			items = append(items, d)
		}
	}
	return items, nil
}

// textEscaper 编码时正文的转义：只转义 & < > "，其余原样写出。
var textEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// Encode 把弹幕按 items 的顺序写成 B 站的 XML 弹幕，是 Decode 的逆过程：编码再解码得到同样的弹幕。
// 每条为 <d p="时间,模式,25,颜色,0,0,,原始 ID,0">：时间为秒、精确到毫秒，模式、颜色（十进制 RGB）与原始 ID 用保存的值，
// 字号、发送时间、弹幕池、发送者哈希、屏蔽等级是固定的默认值。
// 正文不做清洗：XML 不允许的字符原样写出。
func Encode(w io.Writer, items []danmaku.Danmaku) error {
	bw := bufio.NewWriter(w)
	if _, err := bw.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<i>\n"); err != nil {
		return err
	}
	var line []byte
	for _, d := range items {
		line = append(line[:0], `<d p="`...)
		line = strconv.AppendFloat(line, float64(d.TimeMs)/1000, 'f', 3, 64)
		line = append(line, ',')
		line = strconv.AppendUint(line, uint64(d.Mode), 10)
		line = append(line, ",25,"...)
		line = strconv.AppendUint(line, uint64(d.Color), 10)
		line = append(line, ",0,0,,"...)
		line = strconv.AppendInt(line, d.SourceID, 10)
		line = append(line, `,0">`...)
		line = append(line, textEscaper.Replace(d.Text)...)
		line = append(line, "</d>\n"...)
		if _, err := bw.Write(line); err != nil {
			return err
		}
	}
	if _, err := bw.WriteString("</i>\n"); err != nil {
		return err
	}
	return bw.Flush()
}

// trimTrailingComments 去掉文档末尾的空白和 XML 注释。有些流传的弹幕文件在 </i> 之后附了注释
// （例如为了让下载工具能下载，凑够 1 KB），仍是完整的文档。
func trimTrailingComments(b []byte) []byte {
	for {
		b = bytes.TrimSpace(b)
		i := bytes.LastIndex(b, []byte("<!--"))
		if !bytes.HasSuffix(b, []byte("-->")) || i < 0 {
			return b
		}
		b = b[:i]
	}
}

// decodeElem p 用逗号分隔：时间（秒，带小数）、模式、字号、颜色（十进制 RGB）、发送时间、弹幕池、发送者哈希、
// 原始弹幕 ID、屏蔽等级。只用时间、模式、颜色和 ID；时间换算成毫秒，四舍五入。
func decodeElem(p, text string) (danmaku.Danmaku, bool) {
	f := strings.Split(p, ",")
	if len(f) < 8 {
		return danmaku.Danmaku{}, false
	}
	seconds, err1 := strconv.ParseFloat(f[0], 64)
	mode, err2 := strconv.ParseInt(f[1], 10, 64)
	color, err3 := strconv.ParseUint(f[3], 10, 64)
	id, err4 := strconv.ParseInt(f[7], 10, 64)
	ms := math.Round(seconds * 1000)
	if errors.Join(err1, err2, err3, err4) != nil || !(ms >= math.MinInt32 && ms <= math.MaxInt32) { // 后者也排除了 NaN
		return danmaku.Danmaku{}, false
	}
	return NewDanmaku(id, int32(ms), mode, color, text)
}

// NewDanmaku 把 B 站一条弹幕的字段映射成内部格式，protobuf 与 XML 共用；不是普通的文字弹幕、没有 ID 或正文为空时 ok 为 false。
//   - 模式：1、2、3 合并为滚动，4、5、6 原样保留，7、8、9（高级、代码、BAS）和其他取值丢弃；
//   - 颜色：只取低 24 位，大会员渐变色忽略；
//   - 正文：去掉非法的 UTF-8 字节和 NUL（PostgreSQL 的 text 存不了），以及 XML 1.0 不允许的字符（导出的 XML 要能被
//     严格的解析器打开），其余原样保留；去掉空白后为空的丢弃。
func NewDanmaku(id int64, timeMs int32, mode int64, color uint64, text string) (danmaku.Danmaku, bool) {
	d := danmaku.Danmaku{SourceID: id, TimeMs: timeMs, Color: uint32(color & 0xFFFFFF)}
	switch mode {
	case 1, 2, 3:
		d.Mode = danmaku.ModeScroll
	case 4, 5, 6:
		d.Mode = danmaku.Mode(mode)
	default:
		return danmaku.Danmaku{}, false
	}
	d.Text = strings.Map(dropXMLInvalid, strings.ToValidUTF8(text, ""))
	if d.SourceID == 0 || strings.TrimSpace(d.Text) == "" {
		return danmaku.Danmaku{}, false
	}
	return d, true
}

// dropXMLInvalid 供 strings.Map 用：XML 1.0 不允许的字符（含 NUL）返回 -1 去掉。
// 合法的 UTF-8 里不会出现代理项，超出 U+10FFFF 的码点也不会出现，所以只需排除 C0 控制字符（制表、换行、回车除外）与 U+FFFE、U+FFFF。
func dropXMLInvalid(r rune) rune {
	if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0xFFFE || r == 0xFFFF {
		return -1
	}
	return r
}
