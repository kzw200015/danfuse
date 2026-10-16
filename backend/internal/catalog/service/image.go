package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/kzw200015/danfuse/backend/internal/catalog/dao"
)

// GetImage 一张图片的原始字节与 content-type；不存在时返回 404。
func (s *Service) GetImage(ctx context.Context, id int64) (dao.GetImageRow, error) {
	img, err := s.q.GetImage(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return dao.GetImageRow{}, errImageNotFound
		}
		return dao.GetImageRow{}, fmt.Errorf("get image %d: %w", id, err)
	}
	return img, nil
}
