package service

import (
	"context"
	"fmt"
	"time"

	"github.com/kzw200015/danfuse/backend/internal/binding/dao"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// View 绑定的 JSON。不对外暴露原始 ref。sourceUrl / sourceLabel：贴链接建的由适配器的
// Describe 生成；用弹幕文件建的没有链接，标签写明文件的份数（季绑定建出的为条目的文件份数）。
type View struct {
	ID           int64   `json:"id"`
	Kind         string  `json:"kind"`        // link | file
	Adapter      *string `json:"adapter"`     // 用弹幕文件建的为 null
	SourceURL    *string `json:"sourceUrl"`   // 用弹幕文件建的为 null
	SourceLabel  string  `json:"sourceLabel"` // 例如"B 站投稿 BV1xx411c7XX P2"、"弹幕文件 · 5 份"
	Title        string  `json:"title"`       // 弹幕源的标题；季绑定建出的文件绑定为条目名称
	Duration     *int32  `json:"duration"`    // 弹幕源视频的时长，秒；用弹幕文件建的没有时长，为 null
	Offset       float64 `json:"offset"`      // 秒，正数表示弹幕延后
	Status       string  `json:"status"`      // active | dead
	DanmakuCount int32   `json:"danmakuCount"`
	// ContentVersion 弹幕内容的版本：插入了新弹幕、或替换了全部弹幕时加 1，改偏移不变。管理界面据此刷新已打开的弹幕列表
	ContentVersion int32 `json:"contentVersion"`
	// MaxTimeMs 最晚一条弹幕的时间（毫秒，未校正），管理界面查看弹幕时作为拖动条的长度；没有弹幕时为 0
	MaxTimeMs     int32      `json:"maxTimeMs"`
	LastFetchedAt *time.Time `json:"lastFetchedAt"`
	// SeasonBindingID 建出这个绑定的季绑定；在集上手动建的为 null
	SeasonBindingID *int64 `json:"seasonBindingId"`
}

// ListBySeries 一部剧的全部绑定，按集 ID 分组，组内按创建顺序。剧详情用。
func (s *Service) ListBySeries(ctx context.Context, seriesID int64) (map[int64][]View, error) {
	rows, err := s.q.ListBindingsBySeries(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("list bindings of series %d: %w", seriesID, err)
	}
	views := make(map[int64][]View) // 集 ID → 这一集的绑定
	for _, r := range rows {
		v, err := s.describe(r.Binding, r.EntryFileCount)
		if err != nil {
			return nil, err
		}
		views[r.Binding.EpisodeID] = append(views[r.Binding.EpisodeID], v)
	}
	return views, nil
}

// view 绑定的 JSON（见 describe）：季绑定建出的文件绑定另查条目的文件份数。
func (s *Service) view(ctx context.Context, b dao.Binding) (View, error) {
	var entryFiles int32
	if b.EntryID != nil {
		var err error
		if entryFiles, err = s.q.CountEntryFiles(ctx, *b.EntryID); err != nil {
			return View{}, fmt.Errorf("count entry files of binding %d: %w", b.ID, err)
		}
	}
	return s.describe(b, entryFiles)
}

// describe 绑定的 JSON：贴链接建的，弹幕源的链接和标签交给它的适配器生成；用弹幕文件建的，标签写明文件的份数，
// 季绑定建出的文件绑定的文件在条目上，份数为 entryFiles。
func (s *Service) describe(b dao.Binding, entryFiles int32) (View, error) {
	v := View{
		ID:              b.ID,
		Kind:            b.Kind,
		Adapter:         b.Adapter,
		Title:           b.Title,
		Duration:        b.Duration,
		Offset:          b.Offset,
		Status:          b.Status,
		DanmakuCount:    b.DanmakuCount,
		ContentVersion:  b.ContentVersion,
		MaxTimeMs:       b.MaxTimeMs,
		LastFetchedAt:   b.LastFetchedAt,
		SeasonBindingID: b.SeasonBindingID,
	}
	if b.Kind == kindFile {
		files := b.FileCount
		if b.EntryID != nil {
			files = entryFiles
		}
		v.SourceLabel = fmt.Sprintf("弹幕文件 · %d 份", files)
		return v, nil
	}
	adapter, err := s.linkAdapter(b)
	if err != nil {
		return View{}, err
	}
	d, err := adapter.Describe(b.Ref)
	if err != nil {
		return View{}, fmt.Errorf("describe binding %d: %w", b.ID, err)
	}
	v.SourceURL, v.SourceLabel = &d.URL, d.Label
	return v, nil
}

// platform 绑定的弹幕所在的平台：贴链接建的取自适配器；弹幕文件里的弹幕不属于任何平台（见 docs/adr/0004）。
func (s *Service) platform(b dao.Binding) (danmaku.Platform, error) {
	if b.Kind == kindFile {
		return danmaku.PlatformNone, nil
	}
	adapter, err := s.linkAdapter(b)
	if err != nil {
		return "", err
	}
	return adapter.Platform(), nil
}
