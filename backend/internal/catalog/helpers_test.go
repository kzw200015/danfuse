package catalog_test

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog/catalogdb"
	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// holdSyncLock 模拟另一个实例正在同步：拿走同步的租约。返回的 unlock 可以重复调用，测试结束时也会自动调用。
func holdSyncLock(t *testing.T, pool *pgxpool.Pool) (unlock func()) {
	t.Helper()
	lease, ok, err := database.TryLease(t.Context(), pool, testenv.Logger(t), database.LeaseSync)
	if err != nil || !ok {
		t.Fatalf("TryLease = %v, %v", ok, err)
	}
	t.Cleanup(lease.Release) // 在关闭连接池之前：释放要用连接池
	return lease.Release
}

// catalogRow 目录里的一集，连同它所在的季和剧。
type catalogRow struct {
	ids  [3]int64 // 剧、季、集的 id
	text string   // "类型|剧名|原名|年份 / 季号|季标题 / 集号|集标题|时长"，空值写作 -
}

// readCatalog 读出目录里的全部集，按剧、季、集的 id 排序。
func readCatalog(t *testing.T, pool *pgxpool.Pool) []catalogRow {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT s.id, se.id, e.id,
		       format('%s|%s|%s|%s / %s|%s / %s|%s|%s',
		              s.type, s.title, coalesce(s.original_title, '-'), coalesce(s.year::text, '-'),
		              se.number, coalesce(se.title, '-'),
		              e.number, coalesce(e.title, '-'), coalesce(e.duration::text, '-'))
		FROM series s
		JOIN seasons se ON se.series_id = s.id
		JOIN episodes e ON e.season_id = se.id
		ORDER BY s.id, se.id, e.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []catalogRow
	for rows.Next() {
		var r catalogRow
		if err := rows.Scan(&r.ids[0], &r.ids[1], &r.ids[2], &r.text); err != nil {
			t.Fatal(err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func texts(rows []catalogRow) []string {
	result := make([]string, len(rows))
	for i, r := range rows {
		result[i] = r.text
	}
	return result
}

// readSeries 目录里的全部剧，按 id 排序："id|类型|标题|年份|TMDB ID"，空值写作 -。
func readSeries(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT format('%s|%s|%s|%s|%s', id, type, title, coalesce(year::text, '-'), coalesce(tmdb_id::text, '-'))
		FROM series
		ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	result, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// poster 一部剧的海报：图片 ID（没有海报时为 0），以及 "content-type|内容"（没有海报时为空）。
type poster struct {
	id   int64
	text string
}

// readPoster 读出剧名为 title 的那部剧（目录里只有一部同名的剧）的海报。
func readPoster(t *testing.T, pool *pgxpool.Pool, title string) poster {
	t.Helper()
	var p poster
	err := pool.QueryRow(t.Context(), `
		SELECT coalesce(i.id, 0), coalesce(i.content_type || '|' || convert_from(i.data, 'UTF8'), '')
		FROM series s
		LEFT JOIN images i ON i.id = s.poster_image_id
		WHERE s.title = $1`, title).Scan(&p.id, &p.text)
	if err != nil {
		t.Fatalf("读出「%s」的海报：%v", title, err)
	}
	return p
}

// runCounts 同步记录的状态、进度与新增数，例如 "succeeded 2/2 新增剧 2 季 3 集 4"，便于整体比较。
func runCounts(run catalogdb.SyncRun) string {
	total := "-"
	if run.Total != nil {
		total = fmt.Sprint(*run.Total)
	}
	return fmt.Sprintf("%s %s/%d 新增剧 %d 季 %d 集 %d", run.Status, total, run.Done,
		run.CreatedSeries, run.CreatedSeasons, run.CreatedEpisodes)
}
