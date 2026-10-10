package binding

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kzw200015/danfuse/backend/internal/source"
)

// SeasonUploadPreview 按季上传的预览：各个条目按集号规则认出的序号，顺序与请求里的条目名称相同。
type SeasonUploadPreview struct {
	Items []SeasonUploadPreviewItem `json:"items"`
}

// SeasonUploadPreviewItem 预览的一个条目，字段与季绑定预览的条目相同；重复的序号已标为对不上（number 为 null、reason 为"集号重复"）。
type SeasonUploadPreviewItem struct {
	Label  string  `json:"label"`
	Number *int    `json:"number"`
	Reason *string `json:"reason"` // 对不上的原因
}

// PreviewSeasonUpload 按季上传的预览：把各个条目当作按规则编号的合集里的条目（名称就是标签），
// 用集号规则认出序号，与季绑定预览同一套匹配、NFKC 回退和"集号重复"的标注。名称相同的条目各自保留。
// 集号对应、对到哪一集由调用方按序号现算。不写库；季不存在为 404。
func (s *Service) PreviewSeasonUpload(ctx context.Context, seasonID int64, labels []string, rule source.EpisodeRule) (SeasonUploadPreview, error) {
	switch exists, err := s.q.SeasonExists(ctx, seasonID); {
	case err != nil:
		return SeasonUploadPreview{}, fmt.Errorf("check season %d: %w", seasonID, err)
	case !exists:
		return SeasonUploadPreview{}, errSeasonNotFound
	}

	col := source.Collection{NumberedByRule: true, Items: make([]source.CollectionItem, len(labels))}
	for i, label := range labels {
		// 条目没有弹幕源，ref 只用来让每个条目各不相同（NumberItems 会去掉 ref 重复的条目）
		col.Items[i] = source.CollectionItem{Ref: source.Ref(strconv.Itoa(i)), Label: label}
	}
	items := source.NumberItems(col, rule)
	preview := SeasonUploadPreview{Items: make([]SeasonUploadPreviewItem, len(items))}
	for i, it := range items {
		item := SeasonUploadPreviewItem{Label: it.Label}
		if it.Unmatched == "" {
			item.Number = new(it.Number)
		} else {
			item.Reason = new(it.Unmatched)
		}
		preview.Items[i] = item
	}
	return preview, nil
}
