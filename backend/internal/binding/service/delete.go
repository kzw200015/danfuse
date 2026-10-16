package service

import (
	"context"
	"fmt"
)

// Delete 删除绑定，它的弹幕随外键级联删除。单条语句；进行中的拉取写回时会发现绑定已被删除。
// 季绑定建出的绑定不删，返回 409（见 checkNotBuilt）。
func (s *Service) Delete(ctx context.Context, id int64) error {
	n, err := s.q.DeleteBinding(ctx, id)
	if err != nil {
		return fmt.Errorf("delete binding %d: %w", id, err)
	}
	if n > 0 {
		return nil
	}
	b, err := s.getBinding(ctx, id)
	if err != nil {
		return err
	}
	return s.checkNotBuilt(ctx, b)
}
