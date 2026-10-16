package service

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// Comments 一集合并后的全部弹幕（danmaku.Merge），去掉了正文命中屏蔽词的。集不存在或没有绑定时返回空。
// 弹幕（binding.Service.EpisodeTracks）与屏蔽词同时读，屏蔽词不多占一次数据库往返的时间。
func (d *Service) Comments(ctx context.Context, episodeID int64) ([]danmaku.Item, error) {
	var (
		tracks    []danmaku.Track
		blocklist danmaku.Blocklist
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		tracks, err = d.bindings.EpisodeTracks(gctx, episodeID)
		return err
	})
	g.Go(func() (err error) {
		blocklist, err = d.blockedWords.Blocklist(gctx)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, nil
	}
	return danmaku.Merge(tracks, blocklist), nil
}
