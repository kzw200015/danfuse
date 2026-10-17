package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/danmaku/bilifmt"
	"github.com/kzw200015/danfuse/backend/internal/export/dao"
)

// ExportBinding 导出一个绑定：一份 <绑定标题>.xml，内容是 bindingDanmaku。失效的、季绑定建出的绑定照常导出，
// 没有弹幕时是一份空的 XML。绑定不存在时 404，不调用 open。
// 读完全部弹幕、事务结束之后才调用 open 写出：读取出错时还能照常返回错误响应，不会写出一半的 XML。
// 一个绑定的量放得进内存，用不着集、季那样边读边写。
func (s *Service) ExportBinding(ctx context.Context, id int64, open Open) error {
	var title string
	var items []danmaku.Danmaku
	err := s.snapshot(ctx, func(q *dao.Queries) error {
		b, err := q.GetBinding(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errBindingNotFound
		}
		if err != nil {
			return fmt.Errorf("get binding %d: %w", id, err)
		}
		title = b.Title
		items, err = bindingDanmaku(ctx, q, id, b.Offset)
		return err
	})
	if err != nil {
		return err
	}
	if err := bilifmt.Encode(open(bindingName(title)+".xml"), items); err != nil {
		return fmt.Errorf("write binding %d: %w", id, err)
	}
	return nil
}

// bindingDanmaku 一个绑定保存的全部弹幕，按偏移校正到本地视频的时间轴（danmaku.Correct，校正后为负的丢弃），
// 按校正后的时间升序、同一时间按原始 ID（ListDanmakuByBinding 的顺序，校正不改变先后）。
func bindingDanmaku(ctx context.Context, q *dao.Queries, bindingID int64, offset float64) ([]danmaku.Danmaku, error) {
	rows, err := q.ListDanmakuByBinding(ctx, bindingID)
	if err != nil {
		return nil, fmt.Errorf("list danmaku of binding %d: %w", bindingID, err)
	}
	items := make([]danmaku.Danmaku, 0, len(rows))
	for _, r := range rows {
		d := danmaku.Danmaku{TimeMs: r.TimeMs, Mode: danmaku.Mode(r.Mode), Color: uint32(r.Color), Text: r.Text, SourceID: r.SourceID}
		if d, ok := danmaku.Correct(d, offset); ok {
			items = append(items, d)
		}
	}
	return items, nil
}
