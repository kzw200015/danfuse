package catalog

import "context"

var (
	ErrSyncRunning     = errSyncRunning
	ErrNoCatalogSource = errNoCatalogSource
)

// StartScheduled 请 Run 的循环开始一次定时同步（见 startScheduled），等它返回。
func (s *SyncService) StartScheduled(ctx context.Context) error {
	return s.loop.Call(ctx, func(ctx context.Context) error {
		s.startScheduled(ctx)
		return nil
	})
}
