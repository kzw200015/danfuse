package testenv

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/catalog/catalogdb"
	"github.com/kzw200015/danfuse/backend/internal/config"
)

// FakeCatalog 实现 catalog.Source 的假目录源：List 返回 Items 的清单，Items 依次产出 Items。
type FakeCatalog struct {
	Warnings []string
	Items    []catalog.Item
	ListErr  error         // List 直接返回这个错误
	FailAt   int           // 大于 0 时，第 FailAt 项（从 1 数起）改为产出请求失败的错误 ErrSourceRequest
	Gate     chan struct{} // 不为 nil 时，每产出一项之前等一次放行
}

var _ catalog.Source = (*FakeCatalog)(nil)

// ErrSourceRequest FakeCatalog 在第 FailAt 项产出的错误。
var ErrSourceRequest = &catalog.Error{Message: "目录源请求失败", Err: errors.New("GET /Items: 500 Internal Server Error")}

func (f *FakeCatalog) List(ctx context.Context) (catalog.Listing, error) {
	if f.ListErr != nil {
		return catalog.Listing{}, f.ListErr
	}
	items := f.Items
	return catalog.Listing{
		Total:    len(items),
		Warnings: f.Warnings,
		Items: func(yield func(catalog.Item, error) bool) {
			for i, item := range items {
				if f.Gate != nil {
					select {
					case <-f.Gate:
					case <-ctx.Done(): // 与真实的适配一样：ctx 取消时请求失败
						yield(catalog.Item{}, ctx.Err())
						return
					}
				}
				if i+1 == f.FailAt {
					yield(catalog.Item{}, ErrSourceRequest)
					return
				}
				if !yield(item, nil) {
					return
				}
			}
		},
	}, nil
}

// TV 一部剧集。
func TV(title string, year *int, seasons ...catalog.Season) *catalog.Series {
	return &catalog.Series{Type: catalog.TypeTV, Title: title, Year: year, Seasons: seasons}
}

// Movie 一部电影：只有一季一集，集的时长为 duration 秒。
func Movie(title string, year *int, duration int) *catalog.Series {
	return &catalog.Series{Type: catalog.TypeMovie, Title: title, Year: year, Seasons: []catalog.Season{
		{Number: 1, Episodes: []catalog.Episode{{Number: 1, Duration: &duration}}},
	}}
}

func Season(number int, title string, episodes ...catalog.Episode) catalog.Season {
	return catalog.Season{Number: number, Title: title, Episodes: episodes}
}

// Episode 一集，时长为 duration 秒。
func Episode(number int, title string, duration int) catalog.Episode {
	return catalog.Episode{Number: number, Title: title, Duration: &duration}
}

// Item 目录源产出的一部剧，Name 取剧名。
func Item(s *catalog.Series, warnings ...string) catalog.Item {
	return catalog.Item{Name: s.Title, Series: s, Warnings: warnings}
}

// StartSync 构造不开定时同步的 SyncService 并在后台运行 Run（要在 synctest 的气泡里调用）。src 为 nil 表示未配置目录源。
func StartSync(t *testing.T, pool *pgxpool.Pool, src catalog.Source) *catalog.SyncService {
	t.Helper()
	svc := catalog.NewSyncService(pool, src, config.Defaults().Sync, Logger(t))
	RunInBackground(t, svc)
	return svc
}

// SyncOnce 手动触发一次同步，等它结束后返回同步记录。
func SyncOnce(t *testing.T, svc *catalog.SyncService) catalogdb.SyncRun {
	t.Helper()
	return GetRun(t, svc, TriggerSync(t, svc))
}

// TriggerSync 手动触发一次同步，等后台停下（同步结束，或停在假目录源的 Gate 上），返回这次同步的 ID。
func TriggerSync(t *testing.T, svc *catalog.SyncService) int64 {
	t.Helper()
	id, err := svc.Trigger(t.Context())
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	synctest.Wait()
	return id
}

func GetRun(t *testing.T, svc *catalog.SyncService, id int64) catalogdb.SyncRun {
	t.Helper()
	run, err := svc.GetRun(t.Context(), id)
	if err != nil {
		t.Fatalf("GetRun(%d): %v", id, err)
	}
	return run
}
