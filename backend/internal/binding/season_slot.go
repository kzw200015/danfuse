package binding

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/binding/dao"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// 季绑定的槽位：一个季绑定在一集上至多建出一个绑定（唯一约束 (season_binding_id, episode_id)）。
// 下面的方法都由 seasonbinding 在自己的写入事务里调用：调用方开事务，先锁住季、再锁住季绑定；这里先锁集，再写绑定。
// 锁多集时不必按固定顺序：同时会锁同一季多集的只有删季，它被调用方先锁的季挡在外面；只删一集时也是先锁集、再删它的绑定；
// 别的写入事务对集只加 FOR KEY SHARE，互不阻塞。

var (
	// ErrEpisodeGone CreateLinkSlotInTx、CreateFileSlotInTx：要写入的集已被删除（例如拉取期间）。
	ErrEpisodeGone = errors.New("episode deleted")
	// ErrSourceBound CreateLinkSlotInTx：集上已有同一个弹幕源的绑定（手动绑的、别的季绑定建的），什么都没写。
	ErrSourceBound = errors.New("source already bound")
)

// LinkSlot 合集的季绑定在一集上建出的链接绑定：弹幕源，以及在事务之外拉取的结果。
type LinkSlot struct {
	SeasonBindingID, EpisodeID int64
	Adapter                    string
	Ref                        []byte
	Fetched                    source.Fetched
	CreatedAt                  time.Time // 建出时间，也是拉取时间；定时拉取按它算窗口
}

// FileSlot 文件夹的季绑定按一个条目在一集上建出的文件绑定。弹幕文件存在条目上，绑定自己没有文件。
type FileSlot struct {
	SeasonBindingID, EpisodeID, EntryID int64
	Title                               string            // 条目名称
	Danmaku                             []danmaku.Danmaku // 条目的各份文件经 danmakufile.Parse 解析、合并后的弹幕
}

// CreateLinkSlotInTx 在季绑定的写入事务里建出一个链接绑定：锁住集（已被删除时返回 ErrEpisodeGone），
// 插入带季绑定的绑定（集上已有同一个弹幕源时返回 ErrSourceBound，什么都不写），写入弹幕。
// 这一集的槽位要先空出来（替换时先 DeleteSlotsInTx），撞上槽位约束是程序错误。提交之后调用 LogFetched。
func (s *Service) CreateLinkSlotInTx(ctx context.Context, tx pgx.Tx, b LinkSlot) (Saved, error) {
	binding, added, err := insertLinkBinding(ctx, s.q.WithTx(tx), dao.InsertLinkBindingParams{
		EpisodeID:       b.EpisodeID,
		Adapter:         b.Adapter,
		Ref:             b.Ref,
		Title:           b.Fetched.Title,
		Duration:        int32(b.Fetched.Duration),
		SeasonBindingID: &b.SeasonBindingID,
		CreatedAt:       b.CreatedAt,
	}, b.Fetched, ErrEpisodeGone)
	if err != nil {
		return Saved{}, err
	}
	return Saved{Added: added, binding: binding, fetched: b.Fetched}, nil
}

// CreateFileSlotInTx 在季绑定的写入事务里按一个条目建出文件绑定：锁住集（已被删除时返回 ErrEpisodeGone），
// 插入带季绑定和条目的绑定（标题为条目名称，file_count 为 0），写入弹幕（按原始 ID 去重）。
// 同一集上已有别的绑定时照常建。这一集的槽位要先空出来，撞上槽位约束是程序错误。
func (s *Service) CreateFileSlotInTx(ctx context.Context, tx pgx.Tx, b FileSlot) error {
	q := s.q.WithTx(tx)
	if err := lockEpisode(ctx, q, b.EpisodeID, ErrEpisodeGone); err != nil {
		return err
	}
	id, err := q.InsertEntryBinding(ctx, dao.InsertEntryBindingParams{
		EpisodeID: b.EpisodeID, Title: b.Title, SeasonBindingID: b.SeasonBindingID, EntryID: b.EntryID,
	})
	if err != nil {
		return fmt.Errorf("insert entry binding of episode %d: %w", b.EpisodeID, err)
	}
	_, _, err = writeDanmaku(ctx, q, id, b.Danmaku, false)
	return err
}

// DeleteSlotsInTx 在季绑定的写入事务里删掉它在这些集上的槽位（建出的绑定，弹幕随之级联删除）：
// 先锁住这些集（随后可能在同一集上建出新的绑定，期间删不掉这一集），再删绑定。
// 已被删除的集、没有槽位的集跳过。
func (s *Service) DeleteSlotsInTx(ctx context.Context, tx pgx.Tx, seasonBindingID int64, episodeIDs []int64) error {
	q := s.q.WithTx(tx)
	if _, err := q.LockEpisodes(ctx, episodeIDs); err != nil {
		return fmt.Errorf("lock episodes of season binding %d: %w", seasonBindingID, err)
	}
	err := q.DeleteSlots(ctx, dao.DeleteSlotsParams{SeasonBindingID: seasonBindingID, EpisodeIds: episodeIDs})
	if err != nil {
		return fmt.Errorf("delete slots of season binding %d: %w", seasonBindingID, err)
	}
	return nil
}
