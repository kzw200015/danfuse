// Package service 绑定的业务层。绑定：一集与一个弹幕源的对应关系。HTTP 在同级的 handler 包，查询在 dao 包（sqlc）。
//   - service.go：Service、错误，以及包内共用的取、锁；view.go：绑定的 JSON 视图、剧详情里按集列出；
//   - offset.go：改偏移；delete.go：删除；
//   - link.go：贴链接创建（拉取弹幕源的全部弹幕落库）、重新拉取、失效与恢复；
//   - file.go：用弹幕文件建的绑定（创建、追加文件、重新解析）；
//   - danmaku_write.go：把弹幕写进绑定，链接、文件、槽位共用；danmaku_read.go：查看保存的弹幕、读取一集各绑定的弹幕；
//   - season_slot.go：季绑定在各集的槽位里建出的绑定（CreateLinkSlotInTx、CreateFileSlotInTx、DeleteSlotsInTx），
//     事务由 seasonbinding 开；建出的绑定不能单独删除、追加文件；
//   - scheduled_fetch.go：定时拉取。
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/binding/dao"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	errEpisodeNotFound = apierr.ErrNotFound.WithMessage("集不存在")
	errEpisodeDeleted  = apierr.ErrNotFound.WithMessage("这一集已被删除")
	errBindingExists   = apierr.ErrConflict.WithMessage("这一集已经绑定过这个弹幕源")
	errBindingNotFound = apierr.ErrNotFound.WithMessage("绑定不存在")
	errBindingDeleted  = apierr.ErrNotFound.WithMessage("绑定已被删除")
	errNotLinkBinding  = apierr.ErrBadRequest.WithMessage("用弹幕文件建的绑定不能重新拉取")
)

// errBuiltBinding 季绑定建出的绑定不能单独删除、追加文件，title 是建出它的季绑定的名称。
func errBuiltBinding(title string) error {
	return apierr.ErrConflict.WithMessage(fmt.Sprintf("这个绑定由季绑定「%s」建出，不能单独删除或追加文件；要改，去季面板编辑季绑定", title))
}

// 绑定的 kind：弹幕源的形态（见 docs/adr/0004）。
const (
	kindLink = "link" // 贴链接或补建出的，按适配器和 ref 能重新拉取
	kindFile = "file" // 上传的一组弹幕文件，没有适配器和 ref
)

// Service 绑定：贴链接创建，拉取弹幕源的全部弹幕落库；重新拉取、改偏移与删除。
// 用弹幕文件建的绑定（创建、追加文件、重新解析）见 file.go，季绑定的槽位见 season_slot.go。
// 拉取（网络请求）都在事务之外，拉完才开写入事务，写入事务的第一句锁住要写的行（创建时锁集，重新拉取时锁绑定）；
// 不加应用层的锁，并发靠行锁、外键级联和唯一约束。
type Service struct {
	pool    *pgxpool.Pool
	q       *dao.Queries
	sources *source.Registry
	logger  *slog.Logger
}

func NewService(pool *pgxpool.Pool, sources *source.Registry, logger *slog.Logger) *Service {
	return &Service{pool: pool, q: dao.New(pool), sources: sources, logger: logger}
}

// lockEpisode 写入事务里锁住一集到提交（FOR KEY SHARE），期间删不掉它；这一集已被删除时返回 gone。
func lockEpisode(ctx context.Context, q *dao.Queries, id int64, gone error) error {
	if _, err := q.LockEpisode(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return gone
		}
		return fmt.Errorf("lock episode %d: %w", id, err)
	}
	return nil
}

// getBinding 取出绑定，不存在时返回 404"绑定不存在"。
func (s *Service) getBinding(ctx context.Context, id int64) (dao.Binding, error) {
	b, err := s.q.GetBinding(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dao.Binding{}, errBindingNotFound
		}
		return dao.Binding{}, fmt.Errorf("get binding %d: %w", id, err)
	}
	return b, nil
}

// lockBinding 重新拉取、追加文件、重新解析的写入事务的第一句：锁住这个绑定到提交，同一个绑定的写入排队执行。
// 绑定已被删除时返回 404"绑定已被删除"。
func lockBinding(ctx context.Context, q *dao.Queries, id int64) error {
	if _, err := q.LockBinding(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errBindingDeleted
		}
		return fmt.Errorf("lock binding %d: %w", id, err)
	}
	return nil
}

// checkNotBuilt 季绑定建出的绑定（随季绑定的表单增删）不能单独删除、追加文件：返回 409，提示写明建出它的季绑定。
func (s *Service) checkNotBuilt(ctx context.Context, b dao.Binding) error {
	if b.SeasonBindingID == nil {
		return nil
	}
	title, err := s.q.GetSeasonBindingTitle(ctx, *b.SeasonBindingID)
	if errors.Is(err, pgx.ErrNoRows) { // 季绑定刚被删除，它建出的绑定随之删除
		return errBindingNotFound
	}
	if err != nil {
		return fmt.Errorf("get season binding %d of binding %d: %w", *b.SeasonBindingID, b.ID, err)
	}
	return errBuiltBinding(title)
}

// emptyIfNull null 读作空串。
func emptyIfNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
