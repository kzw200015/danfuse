package service_test

import (
	"net/http"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// TestSetFollow 开关追更只用于合集的季绑定：文件夹的为 400，不存在为 404；关掉不补建，打开时随即补建一次。
func TestSetFollow(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &testenv.FakeCollector{Collections: map[string]source.Collection{"s": {}}}
		env := newSeasonEnv(t, pool, src, 1)
		id := env.create().ID
		folder := env.folder([]string{"01"}, nil)
		lists := src.ListCount()

		env.setFollow(id, false)
		synctest.Wait()
		if d := env.get(id); d.Follow || src.ListCount() != lists {
			t.Errorf("关掉追更之后 follow = %v、列出了 %d 次", d.Follow, src.ListCount()-lists)
		}
		env.setFollow(id, true)
		synctest.Wait()
		if d := env.get(id); !d.Follow || src.ListCount() != lists+1 {
			t.Errorf("打开追更之后 follow = %v、列出了 %d 次，want 补建一次", d.Follow, src.ListCount()-lists)
		}

		_, err := env.svc.SetFollow(t.Context(), folder, true)
		testenv.AssertAppError(t, err, http.StatusBadRequest, "这不是合集的季绑定")
		_, err = env.svc.SetFollow(t.Context(), 9, true)
		testenv.AssertAppError(t, err, http.StatusNotFound, "季绑定不存在")
	})
}
