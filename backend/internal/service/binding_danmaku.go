package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/pkg/apierr"
	"github.com/kzw200015/danfuse/backend/internal/repository/sqlc"
)

// danmakuPageSize 查看绑定的弹幕时一页的条数。
const danmakuPageSize = 200

var errInvalidDanmakuCursor = apierr.ErrBadRequest.WithMessage("游标不合法")

// DanmakuView 管理界面查看的一条弹幕。时间未校正；不输出原始 ID（可能超出 JS 的安全整数）。
type DanmakuView struct {
	TimeMs int32  `json:"timeMs"` // 相对弹幕源视频开头的毫秒数
	Mode   int16  `json:"mode"`   // 1 滚动、4 底部、5 顶部、6 逆向
	Color  int32  `json:"color"`  // RGB888
	Text   string `json:"text"`
}

// DanmakuPage 一页弹幕。Next 是下一页的游标，没有下一页时为 null。
type DanmakuPage struct {
	Items []DanmakuView `json:"items"`
	Next  *string       `json:"next"`
}

// ListDanmaku 查看绑定保存的弹幕，按弹幕源时间升序（同一时间按原始 ID），从游标 after 之后取一页；after 为空时从头取。
// fromMs 不为 nil 时只取弹幕源时间在它及以后的（跳转），翻页时也照传。
// 绑定不存在时 404，游标不合法时 400。各页分别读取，翻页期间绑定的弹幕变了也不处理。
func (s *BindingService) ListDanmaku(ctx context.Context, id int64, fromMs *int32, after string) (DanmakuPage, error) {
	params := sqlc.ListBindingDanmakuPageParams{BindingID: id, FromMs: fromMs, PageLimit: danmakuPageSize + 1}
	if after != "" {
		timeMs, sourceID, ok := parseDanmakuCursor(after)
		if !ok {
			return DanmakuPage{}, errInvalidDanmakuCursor
		}
		params.AfterTimeMs, params.AfterSourceID = &timeMs, &sourceID
	}
	if _, err := s.getBinding(ctx, id); err != nil {
		return DanmakuPage{}, err
	}
	rows, err := s.store.ListBindingDanmakuPage(ctx, params)
	if err != nil {
		return DanmakuPage{}, fmt.Errorf("list danmaku of binding %d: %w", id, err)
	}

	var page DanmakuPage
	// 多取的一条只用来判断还有没有下一页
	if len(rows) > danmakuPageSize {
		rows = rows[:danmakuPageSize]
		last := rows[len(rows)-1]
		next := formatDanmakuCursor(last.TimeMs, last.SourceID)
		page.Next = &next
	}
	page.Items = make([]DanmakuView, len(rows))
	for i, r := range rows {
		page.Items[i] = DanmakuView{TimeMs: r.TimeMs, Mode: r.Mode, Color: r.Color, Text: r.Text}
	}
	return page, nil
}

// formatDanmakuCursor 游标是一页最后一条的时间与原始 ID，对外不透明。
func formatDanmakuCursor(timeMs int32, sourceID int64) string {
	return strconv.FormatInt(int64(timeMs), 10) + "_" + strconv.FormatInt(sourceID, 10)
}

// parseDanmakuCursor 解析 formatDanmakuCursor 给出的游标，不合法时 ok 为 false。
func parseDanmakuCursor(cursor string) (timeMs int32, sourceID int64, ok bool) {
	t, id, found := strings.Cut(cursor, "_")
	tv, err1 := strconv.ParseInt(t, 10, 32)
	iv, err2 := strconv.ParseInt(id, 10, 64)
	if !found || err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return int32(tv), iv, true
}
