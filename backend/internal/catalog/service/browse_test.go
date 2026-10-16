package service_test

import (
	"log/slog"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/catalog/service"
	"github.com/kzw200015/danfuse/backend/internal/infra/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// TestSourceLinkWithoutCatalogSource 未配置目录源时，存着目录源 ID 的剧也没有链接。
func TestSourceLinkWithoutCatalogSource(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	if _, err := pool.Exec(t.Context(), `INSERT INTO series (type, title, catalog_source_id) VALUES ('tv', '星海旅人', 'jf-1')`); err != nil {
		t.Fatal(err)
	}
	env := testenv.New(pool, slog.New(slog.DiscardHandler))
	svc := service.NewService(pool, nil, env.Bindings, env.SeasonBindings)

	detail, err := svc.GetSeries(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if detail.SourceLink != nil {
		t.Errorf("SourceLink = %+v, want nil", *detail.SourceLink)
	}
}
