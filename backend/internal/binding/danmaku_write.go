package binding

import (
	"context"
	"fmt"
	"time"

	"github.com/kzw200015/danfuse/backend/internal/binding/dao"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// saveFetched 在写入事务里保存一次拉取的结果：更新标题、时长与拉取时间 fetchedAt，再用 writeDanmaku 写入弹幕。
// 调用方已在同一个事务里锁住或刚插入这个绑定。返回更新后的绑定和新增条数（replace 时即这次的总条数）。
//
// 拉取时间（也是上次尝试拉取的时间）取自应用的时钟（拉取完成时的 time.Now()），不用数据库的 now()：
// 定时拉取按它判断是否到期，与建出时间用同一个时钟，测试里也能用假时间推进。
func saveFetched(ctx context.Context, q *dao.Queries, bindingID int64, f source.Fetched, replace bool, fetchedAt time.Time) (dao.Binding, int64, error) {
	err := q.RecordFetch(ctx, dao.RecordFetchParams{
		ID:        bindingID,
		Title:     f.Title,
		Duration:  int32(f.Duration),
		FetchedAt: fetchedAt,
	})
	if err != nil {
		return dao.Binding{}, 0, fmt.Errorf("record fetch of binding %d: %w", bindingID, err)
	}
	return writeDanmaku(ctx, q, bindingID, f.Danmaku, replace)
}

// writeDanmaku 在写入事务里写入一批弹幕并更新计数：replace 时先删掉这个绑定的全部弹幕；
// 插入弹幕（按原始 ID 去重，已有的跳过），再用 recordDanmaku 更新计数。返回更新后的绑定和新增条数。
func writeDanmaku(ctx context.Context, q *dao.Queries, bindingID int64, items []danmaku.Danmaku, replace bool) (dao.Binding, int64, error) {
	if replace {
		if err := q.DeleteDanmaku(ctx, bindingID); err != nil {
			return dao.Binding{}, 0, fmt.Errorf("delete danmaku of binding %d: %w", bindingID, err)
		}
	}
	added, err := insertDanmaku(ctx, q, bindingID, items)
	if err != nil {
		return dao.Binding{}, 0, err
	}
	b, err := recordDanmaku(ctx, q, bindingID, replace, added, latestTime(items))
	return b, added, err
}

// recordDanmaku 写入弹幕之后更新绑定的 danmaku_count、content_version 与 max_time_ms（规则见 RecordDanmaku），返回更新后的绑定。
// latestMs 是这次写入的整批弹幕的 latestTime。
func recordDanmaku(ctx context.Context, q *dao.Queries, bindingID int64, replace bool, added int64, latestMs int32) (dao.Binding, error) {
	b, err := q.RecordDanmaku(ctx, dao.RecordDanmakuParams{
		ID: bindingID, Replace: replace, Added: int32(added), LatestMs: latestMs,
	})
	if err != nil {
		return dao.Binding{}, fmt.Errorf("record danmaku of binding %d: %w", bindingID, err)
	}
	return b, nil
}

// latestTime 一批弹幕里最晚的时间，不早于 0（没有弹幕时为 0）。
func latestTime(items []danmaku.Danmaku) int32 {
	var latest int32
	for _, d := range items {
		latest = max(latest, d.TimeMs)
	}
	return latest
}

// insertDanmaku 一条语句写入一批弹幕，按原始 ID 去重（已有的跳过），返回实际插入的条数。
func insertDanmaku(ctx context.Context, q *dao.Queries, bindingID int64, items []danmaku.Danmaku) (int64, error) {
	p := dao.InsertDanmakuParams{
		BindingID: bindingID,
		SourceIds: make([]int64, len(items)),
		TimeMs:    make([]int32, len(items)),
		Modes:     make([]int16, len(items)),
		Colors:    make([]int32, len(items)),
		Texts:     make([]string, len(items)),
	}
	for i, d := range items {
		p.SourceIds[i] = d.SourceID
		p.TimeMs[i] = d.TimeMs
		p.Modes[i] = int16(d.Mode)
		p.Colors[i] = int32(d.Color)
		p.Texts[i] = d.Text
	}
	added, err := q.InsertDanmaku(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("insert danmaku of binding %d: %w", bindingID, err)
	}
	return added, nil
}
