package service_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/export/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// newService 新库里写入 testenv.SeedEpisodes 的剧（第 1 季，集 1、2），返回导出的 service。
func newService(t *testing.T) (*service.Service, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.Pool(t)
	testenv.SeedEpisodes(t, pool)
	return testenv.New(pool, testenv.Logger(t)).Export, pool
}

// insertBinding 给集 1 写入一个链接绑定和它的弹幕（danmaku_count、max_time_ms 与弹幕一致），返回绑定 ID。
// ref 用标题区分，同一集里标题不重复即可。
func insertBinding(t *testing.T, pool *pgxpool.Pool, title string, offset float64, ds ...danmaku.Danmaku) int64 {
	t.Helper()
	var maxMs int32
	for _, d := range ds {
		maxMs = max(maxMs, d.TimeMs)
	}
	var id int64
	err := pool.QueryRow(t.Context(), `
		INSERT INTO bindings (episode_id, adapter, ref, title, duration, "offset", danmaku_count, max_time_ms)
		VALUES (1, 'fake', jsonb_build_object('name', $1::text), $1, 1420, $2, $3, $4)
		RETURNING id`, title, offset, len(ds), maxMs).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		_, err := pool.Exec(t.Context(), `INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text) VALUES ($1, $2, $3, $4, $5, $6)`,
			id, d.SourceID, d.TimeMs, int16(d.Mode), int32(d.Color), d.Text)
		if err != nil {
			t.Fatal(err)
		}
	}
	return id
}
