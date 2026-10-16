package service_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/dandan/service"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// TestSeason 按 ID 取到的季与搜索结果里的同一季相同（剧集、电影、特别篇、没有年份）；季不存在时 found 为 false。
func TestSeason(t *testing.T) {
	t.Parallel()
	testenv.SyncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		testenv.SyncOnce(t, testenv.StartSync(t, pool, &testenv.FakeCatalog{Items: searchCatalog()}))
		svc := testenv.New(pool, testenv.Logger(t)).Dandan

		result, err := svc.Search(t.Context(), service.SearchQuery{Keyword: "星海旅人", MaxSeasons: 50})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Seasons) != 7 {
			t.Fatalf("搜到 %d 季，want 7", len(result.Seasons))
		}
		for _, want := range result.Seasons {
			got, found, err := svc.Season(t.Context(), want.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !found || describe(got) != describe(want) || got.ID != want.ID {
				t.Errorf("Season(%d) = %q, found %v; want %q", want.ID, describe(got), found, describe(want))
			}
		}

		if got, found, err := svc.Season(t.Context(), 999999); err != nil || found {
			t.Errorf("Season(999999) = %q, found %v, err %v; want 不存在", describe(got), found, err)
		}
	})
}
