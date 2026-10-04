package bilibili

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// xmlDanmaku 拉取 cid 的 XML 弹幕：实时弹幕池里最新的一批，与未登录时 protobuf 返回的高权重子集几乎不重叠，合并后覆盖面大得多。
// 响应是 JSON 时是 B 站的错误，见 binaryError。弹幕已关闭的视频返回没有弹幕的 XML，算成功。
func (a *Adapter) xmlDanmaku(ctx context.Context, cid int64) ([]danmaku.Danmaku, error) {
	target := fmt.Sprintf("%s/%d.xml", commentURL, cid)
	var items []danmaku.Danmaku
	err := a.client.retry(ctx, func() error {
		r, err := a.client.get(ctx, target)
		switch {
		case err != nil:
			return err
		case r.status != http.StatusOK:
			return sourceError(source.Upstream, fmt.Errorf("GET %s: unexpected status %d", target, r.status))
		case r.isJSON:
			return binaryError(target, r.body)
		}
		if items, err = decodeXML(r.body); err != nil {
			return sourceError(source.Upstream, fmt.Errorf("GET %s: decode XML: %w", target, err))
		}
		return nil
	})
	return items, err
}

// xmlElem 一条弹幕：<d p="属性">正文</d>。p 之外还可以有别的属性，顺序不限；正文里的 < 和 & 都已转义。
var xmlElem = regexp.MustCompile(`<d\s(?:[^>]*?\s)?p="([^"]*)"[^>]*>([^<]*)</d>`)

// decodeXML 解析 XML 弹幕并映射成内部格式，映射规则与 protobuf 相同（newDanmaku）。
// 不用 encoding/xml：它遇到非法的 UTF-8 字节或 XML 不允许的控制字符（包括 &#8; 这样的字符引用）就整份报错，
// 一条坏弹幕会让整次拉取失败（与 protobuf 不用生成代码同理）。这里逐条匹配 <d>，属性与正文按 HTML 的规则反转义，
// p 的内容不对的单条丢弃。要求文档完整（以 </i> 结尾），被截断的响应不会只解出一部分弹幕；
// 有 <d> 却一条都认不出时说明格式变了，按错误处理，不悄悄变成 0 条。
func decodeXML(b []byte) ([]danmaku.Danmaku, error) {
	if !bytes.HasSuffix(bytes.TrimSpace(b), []byte("</i>")) {
		return nil, errors.New("not a complete danmaku XML document")
	}
	elems := xmlElem.FindAllSubmatch(b, -1)
	if len(elems) == 0 && bytes.Contains(b, []byte("<d ")) {
		return nil, errors.New("no recognizable <d> element: the XML format may have changed")
	}
	var items []danmaku.Danmaku
	for _, m := range elems {
		if d, ok := decodeXMLElem(html.UnescapeString(string(m[1])), html.UnescapeString(string(m[2]))); ok {
			items = append(items, d)
		}
	}
	return items, nil
}

// decodeXMLElem p 用逗号分隔：时间（秒，带小数）、模式、字号、颜色（十进制 RGB）、发送时间、弹幕池、发送者哈希、
// 原始弹幕 ID、屏蔽等级。只用时间、模式、颜色和 ID；时间换算成毫秒，四舍五入。
func decodeXMLElem(p, text string) (danmaku.Danmaku, bool) {
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
	return newDanmaku(id, int32(ms), mode, color, text)
}

// mergeXML 把 XML 的弹幕并入 protobuf 的：按原始 ID 去重，两边都有的保留 protobuf 那条，XML 独有的接在后面。
// overlap 为 XML 里与 protobuf 重复的条数，即两者的交集。同一弹幕源内部的重复留给写入时的 ON CONFLICT DO NOTHING。
func mergeXML(proto, xml []danmaku.Danmaku) (merged []danmaku.Danmaku, overlap int) {
	inProto := make(map[int64]bool, len(proto))
	for _, d := range proto {
		inProto[d.SourceID] = true
	}
	merged = proto
	for _, d := range xml {
		if inProto[d.SourceID] {
			overlap++
		} else {
			merged = append(merged, d)
		}
	}
	return merged, overlap
}
