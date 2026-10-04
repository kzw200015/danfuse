// Package migrations goose 的 Go 迁移：编译进二进制，在 init 里注册，goose 从注册它的文件名取版本号，
// 与同目录内嵌的 SQL 迁移按版本号一起执行，用同一把迁移锁，多实例只执行一次。database 包空导入本包。
//
// 分词规则（fulltext）或搜索列的组成（catalog.SearchVector）改变时，按序号新增一个注册 recomputeSearchVectors 的迁移。
package migrations

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pressly/goose/v3"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
)

func init() {
	goose.AddMigrationContext(recomputeSearchVectors, nil)
}

// recomputeSearchVectors 用 catalog.SearchVector 重算所有季的搜索列。新库上没有季，什么也不做。
func recomputeSearchVectors(ctx context.Context, tx *sql.Tx) error {
	type season struct {
		id                               int64
		seriesType, title, originalTitle string
		number                           int
		seasonTitle                      string
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT se.id, s.type, s.title, coalesce(s.original_title, ''), se.number, coalesce(se.title, '')
		FROM seasons se
		JOIN series s ON s.id = se.series_id`)
	if err != nil {
		return fmt.Errorf("list seasons: %w", err)
	}
	defer rows.Close()
	var seasons []season
	for rows.Next() {
		var s season
		if err := rows.Scan(&s.id, &s.seriesType, &s.title, &s.originalTitle, &s.number, &s.seasonTitle); err != nil {
			return fmt.Errorf("scan season: %w", err)
		}
		seasons = append(seasons, s)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("list seasons: %w", err)
	}

	for _, s := range seasons {
		vector := catalog.SearchVector(catalog.SeriesType(s.seriesType), s.title, s.originalTitle, s.number, s.seasonTitle)
		if _, err := tx.ExecContext(ctx, `UPDATE seasons SET search_vector = $2::tsvector WHERE id = $1`, s.id, vector); err != nil {
			return fmt.Errorf("update search vector of season %d: %w", s.id, err)
		}
	}
	return nil
}
