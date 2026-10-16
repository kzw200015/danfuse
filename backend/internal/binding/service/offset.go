package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/binding/dao"
)

// UpdateOffset 改偏移（秒，正数表示弹幕延后），单条语句，content_version 不变。取值范围由调用方校验。
func (s *Service) UpdateOffset(ctx context.Context, id int64, offset float64) (View, error) {
	b, err := s.q.UpdateBindingOffset(ctx, dao.UpdateBindingOffsetParams{ID: id, Offset: offset})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, errBindingNotFound
		}
		return View{}, fmt.Errorf("update offset of binding %d: %w", id, err)
	}
	return s.view(ctx, b)
}
