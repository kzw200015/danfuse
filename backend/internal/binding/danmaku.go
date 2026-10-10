package binding

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/binding/bindingdb"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/httpx/apierr"
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
func (s *Service) ListDanmaku(ctx context.Context, id int64, fromMs *int32, after string) (DanmakuPage, error) {
	params := bindingdb.ListBindingDanmakuPageParams{BindingID: id, FromMs: fromMs, PageLimit: danmakuPageSize + 1}
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
	rows, err := s.q.ListBindingDanmakuPage(ctx, params)
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

// EpisodeDanmaku 一集的全部弹幕（弹弹 API 的 comment）：读出这一集的绑定（失效的照常参与），再用一条查询取出这些绑定的全部弹幕，
// 按绑定分组，交给 danmaku.Merge 校正、跨源去重、排序。集不存在或没有绑定时返回空。播放时不向平台现取（不做 live 存储模式，见 ADR 0002）。
// 不包事务：所有绑定的弹幕一条 SELECT 读完，不会读到某次重新拉取的一半；查完绑定列表后某个绑定被删除时，它的弹幕读成空的。
func (s *Service) EpisodeDanmaku(ctx context.Context, episodeID int64) ([]danmaku.Item, error) {
	bindings, err := s.q.ListBindingsByEpisode(ctx, episodeID)
	if err != nil {
		return nil, fmt.Errorf("list bindings of episode %d: %w", episodeID, err)
	}
	if len(bindings) == 0 {
		return nil, nil
	}
	tracks := make([]danmaku.Track, len(bindings))
	ids := make([]int64, len(bindings))
	for i, b := range bindings {
		platform, err := s.platform(b)
		if err != nil {
			return nil, err
		}
		tracks[i] = danmaku.Track{BindingID: b.ID, Platform: platform, Offset: b.Offset, Scale: b.Scale}
		ids[i] = b.ID
	}
	rows, err := s.q.ListDanmakuByBindings(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list danmaku of episode %d: %w", episodeID, err)
	}
	byBinding := make(map[int64][]danmaku.Danmaku, len(bindings))
	for _, r := range rows {
		byBinding[r.BindingID] = append(byBinding[r.BindingID],
			danmaku.Danmaku{TimeMs: r.TimeMs, Mode: danmaku.Mode(r.Mode), Color: uint32(r.Color), Text: r.Text, SourceID: r.SourceID})
	}
	for i := range tracks {
		tracks[i].Items = byBinding[tracks[i].BindingID]
	}
	return danmaku.Merge(tracks), nil
}
