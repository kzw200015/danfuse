package bilibili

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

const (
	segmentSeconds     = 360 // 每段 6 分钟
	segmentConcurrency = 3   // 单个绑定内同时请求的段数
)

// segments 拉取 cid 的全部分段，段数为 ceil(时长 / 360)（至少 1 段），按段的顺序拼接。
// 同时请求的段数不超过 segmentConcurrency；任何一段最终失败都整体失败，不返回部分弹幕。
func (a *Adapter) segments(ctx context.Context, cid int64, duration int) ([]danmaku.Danmaku, error) {
	parts := make([][]danmaku.Danmaku, max(1, (duration+segmentSeconds-1)/segmentSeconds))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(segmentConcurrency)
	for i := range parts {
		g.Go(func() error {
			var err error
			parts[i], err = a.segment(ctx, cid, i+1)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return slices.Concat(parts...), nil
}

// segment 拉取第 n 段（从 1 开始）。304 为空段；响应是 JSON 时是 B 站的错误，按错误码归类，但不会是 NotFound。
// 弹幕已关闭的视频返回只有 state=1 的响应，解码结果为空，算成功。
func (a *Adapter) segment(ctx context.Context, cid int64, n int) ([]danmaku.Danmaku, error) {
	target := fmt.Sprintf("/x/v2/dm/web/seg.so?type=1&oid=%d&segment_index=%d", cid, n)
	var items []danmaku.Danmaku
	err := a.client.retry(ctx, func() error {
		r, err := a.client.get(ctx, target)
		switch {
		case err != nil:
			return err
		case r.status == http.StatusNotModified:
			return nil
		case r.isJSON:
			_, err := decodeEnvelope(target, r.body)
			if srcErr, ok := errors.AsType[*source.Error](err); ok && srcErr.Kind == source.NotFound {
				// cid 不存在时 seg.so 与弹幕已关闭的返回相同，不能用来判断弹幕源是否存在：NotFound 只来自元数据
				return sourceError(source.Upstream, srcErr.Err)
			}
			if err == nil {
				err = sourceError(source.Upstream, fmt.Errorf("GET %s: unexpected JSON response", target))
			}
			return err
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

// decodeElem 解码一条 DanmakuElem 并映射成内部格式；不是普通的文字弹幕、没有 ID 或正文为空时 ok 为 false。
//   - 模式：1、2、3 合并为滚动，4、5、6 原样保留，7、8、9（高级、代码、BAS）和其他取值丢弃；
//   - 颜色：只取低 24 位，大会员渐变色忽略；
//   - 正文：只清洗非法的 UTF-8 字节和 NUL（PostgreSQL 的 text 存不了），其余原样保留；去掉空白后为空的丢弃。
func decodeElem(b []byte) (d danmaku.Danmaku, ok bool, err error) {
	var mode uint64
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
			d.SourceID = int64(f.value)
		case f.num == elemProgress:
			d.TimeMs = int32(f.value)
		case f.num == elemMode:
			mode = f.value
		case f.num == elemColor:
			d.Color = uint32(f.value & 0xFFFFFF)
		}
	}

	switch mode {
	case 1, 2, 3:
		d.Mode = danmaku.ModeScroll
	case 4, 5, 6:
		d.Mode = danmaku.Mode(mode)
	default:
		return danmaku.Danmaku{}, false, nil
	}
	d.Text = strings.ReplaceAll(strings.ToValidUTF8(string(content), ""), "\x00", "")
	if d.SourceID == 0 || strings.TrimSpace(d.Text) == "" {
		return danmaku.Danmaku{}, false, nil
	}
	return d, true, nil
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
