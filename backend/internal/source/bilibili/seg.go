package bilibili

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/danmaku/bilifmt"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

const segmentSeconds = 360 // 每段 6 分钟

// segment 拉取 cid 的第 n 段（从 1 开始）。304 为空段；响应是 JSON 时是 B 站的错误，见 binaryError。
// 弹幕已关闭的视频返回只有 state=1 的响应，解码结果为空，算成功。
func (a *Adapter) segment(ctx context.Context, cid int64, n int) ([]danmaku.Danmaku, error) {
	target := fmt.Sprintf("%s/x/v2/dm/web/seg.so?type=1&oid=%d&segment_index=%d", apiURL, cid, n)
	var items []danmaku.Danmaku
	err := a.client.retry(ctx, func() error {
		r, err := a.client.get(ctx, target)
		switch {
		case err != nil:
			return err
		case r.status == http.StatusNotModified:
			return nil
		case r.isJSON:
			return binaryError(target, r.body)
		}
		if items, err = decodeSegment(r.body); err != nil {
			return sourceError(source.Upstream, fmt.Errorf("GET %s: decode protobuf: %w", target, err))
		}
		return nil
	})
	return items, err
}

// DmSegMobileReply 与 DanmakuElem 里用到的字段号。只依赖字段号这一线上协议本身，不用 dm.proto 和生成代码：
// 生成代码对 string 字段做 UTF-8 强校验，一条坏弹幕就会让整段解码失败；dm.proto 的许可也不允许拷进本仓库。
const (
	replyElems   protowire.Number = 1 // repeated DanmakuElem
	elemID       protowire.Number = 1 // int64，原始弹幕 ID
	elemProgress protowire.Number = 2 // int32，毫秒；为 0 时不编码
	elemMode     protowire.Number = 3 // int32
	elemColor    protowire.Number = 5 // uint32，RGB888；黑色（0）时不编码
	elemContent  protowire.Number = 7 // string
)

// decodeSegment 解码一段 DmSegMobileReply，映射成内部格式；未知字段一律跳过。
func decodeSegment(b []byte) ([]danmaku.Danmaku, error) {
	var items []danmaku.Danmaku
	for len(b) > 0 {
		f, rest, err := nextField(b)
		if err != nil {
			return nil, err
		}
		b = rest
		if f.num != replyElems || f.typ != protowire.BytesType {
			continue
		}
		d, ok, err := decodeElem(f.bytes)
		if err != nil {
			return nil, err
		}
		if ok {
			items = append(items, d)
		}
	}
	return items, nil
}

// decodeElem 解码一条 DanmakuElem 并映射成内部格式，映射规则见 bilifmt.NewDanmaku。
func decodeElem(b []byte) (d danmaku.Danmaku, ok bool, err error) {
	var id, mode int64
	var progress int32
	var color uint64
	var content []byte
	for len(b) > 0 {
		var f field
		if f, b, err = nextField(b); err != nil {
			return danmaku.Danmaku{}, false, err
		}
		switch {
		case f.typ == protowire.BytesType && f.num == elemContent:
			content = f.bytes
		case f.typ != protowire.VarintType:
			// 其余用到的字段都是 varint，类型不符的与未知字段一样跳过
		case f.num == elemID:
			id = int64(f.value)
		case f.num == elemProgress:
			progress = int32(f.value)
		case f.num == elemMode:
			mode = int64(int32(f.value)) // int32 的负数编码成 10 字节的 varint
		case f.num == elemColor:
			color = f.value
		}
	}
	d, ok = bilifmt.NewDanmaku(id, progress, mode, color, string(content))
	return d, ok, nil
}

// field 消息里的一个字段：varint 解出值，bytes 取出内容，其他类型只跳过。
type field struct {
	num   protowire.Number
	typ   protowire.Type
	value uint64
	bytes []byte
}

// nextField 读出 b 开头的一个字段，返回剩下的字节。
func nextField(b []byte) (field, []byte, error) {
	num, typ, n := protowire.ConsumeTag(b)
	if n < 0 {
		return field{}, nil, protowire.ParseError(n)
	}
	b = b[n:]
	f := field{num: num, typ: typ}
	switch typ {
	case protowire.VarintType:
		f.value, n = protowire.ConsumeVarint(b)
	case protowire.BytesType:
		f.bytes, n = protowire.ConsumeBytes(b)
	default:
		n = protowire.ConsumeFieldValue(num, typ, b)
	}
	if n < 0 {
		return field{}, nil, protowire.ParseError(n)
	}
	return f, b[n:], nil
}
