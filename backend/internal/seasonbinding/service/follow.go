package service

import (
	"context"
	"fmt"

	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/dao"
)

// SetFollow 开关追更（只有合集的季绑定有），不删绑定、不拿租约；打开时随即在后台补建一次（正在补建时不另起一轮）。
// 不存在时 404，文件夹的季绑定 400。返回详情。
func (s *Service) SetFollow(ctx context.Context, id int64, follow bool) (Detail, error) {
	if _, err := s.getCollection(ctx, id); err != nil {
		return Detail{}, err
	}
	n, err := s.q.SetSeasonBindingFollow(ctx, dao.SetSeasonBindingFollowParams{ID: id, Follow: follow})
	if err != nil {
		return Detail{}, fmt.Errorf("set follow of season binding %d: %w", id, err)
	}
	if n == 0 {
		return Detail{}, errSeasonBindingNotFound
	}
	if follow {
		s.startBackfill(ctx, id)
	}
	return s.Get(ctx, id)
}
