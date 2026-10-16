package service

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Delete 删除季绑定，两种一样，不拿租约：一个事务里先锁住季和它（lockSeasonBinding；进行中的补建写入事务先提交，
// 之后补建再也锁不到它，随即结束），再删掉它；建出的绑定（连同弹幕）、条目表、条目和条目的文件随外键级联删除。
// 不存在时返回 404（删除前季被删除也是）。
func (s *Service) Delete(ctx context.Context, id int64) error {
	sb, err := s.getSeasonBinding(ctx, id, errSeasonBindingNotFound)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := lockSeasonBinding(ctx, q, sb.SeasonID, id); err != nil {
			return err
		}
		if err := q.DeleteSeasonBinding(ctx, id); err != nil {
			return fmt.Errorf("delete season binding %d: %w", id, err)
		}
		return nil
	})
}
