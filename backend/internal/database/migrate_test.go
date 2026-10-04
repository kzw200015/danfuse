package database_test

import (
	"io/fs"
	"testing"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/kzw200015/danfuse/backend/db"
	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
)

// recomputeSearchVectorsVersion 重算搜索列的 Go 迁移 db/migrations/00006_recompute_search_vectors.go。
const recomputeSearchVectorsVersion = 6

// TestRecomputeSearchVectorsMigration 升级前就有的季没有搜索列（加列时的默认值是空的），重算迁移为它们算出来。
func TestRecomputeSearchVectorsMigration(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	ctx := t.Context()

	_, err := pool.Exec(ctx, `
		INSERT INTO series (type, title, original_title, year) VALUES
			('tv', '星海旅人', 'Star Voyager', 2019), -- 剧 1
			('movie', '长夜灯塔', NULL, 2020);        -- 剧 2
		INSERT INTO seasons (series_id, number, title) VALUES
			(1, 1, NULL),       -- 季 1
			(1, 0, 'Specials'), -- 季 2
			(2, 1, NULL);       -- 季 3
	`)
	if err != nil {
		t.Fatal(err)
	}

	// 库已经迁移到最新，不记版本、单独再执行一次重算迁移
	migrations, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, stdlib.OpenDBFromPool(pool), migrations, goose.WithDisableVersioning(true))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.Close() })
	if _, err := provider.ApplyVersion(ctx, recomputeSearchVectorsVersion, true); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		seasonID int64
		want     string
	}{
		{1, catalog.SearchVector(catalog.TypeTV, "星海旅人", "Star Voyager", 1, "")},
		{2, catalog.SearchVector(catalog.TypeTV, "星海旅人", "Star Voyager", 0, "Specials")},
		{3, catalog.SearchVector(catalog.TypeMovie, "长夜灯塔", "", 1, "")},
	} {
		var got string
		var equal bool
		err := pool.QueryRow(ctx, `SELECT search_vector::text, search_vector = $2::tsvector FROM seasons WHERE id = $1`,
			tt.seasonID, tt.want).Scan(&got, &equal)
		if err != nil {
			t.Fatal(err)
		}
		if !equal {
			t.Errorf("季 %d 的搜索列 = %s, want %s", tt.seasonID, got, tt.want)
		}
	}
}
