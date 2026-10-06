package bilibili

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/danmaku/bilifmt"
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
		if items, err = bilifmt.Decode(r.body); err != nil {
			return sourceError(source.Upstream, fmt.Errorf("GET %s: decode XML: %w", target, err))
		}
		return nil
	})
	return items, err
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
