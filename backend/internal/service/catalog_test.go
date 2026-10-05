package service

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/repository"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// catalogItems 目录源里的两部剧。在新库里同步一次后，ID 按写入顺序分配：
//   - 剧 1「星海旅人」，海报是图片 1；季 1 是第 1 季（集 1、集 2），季 2 是第 2 季（集 3）；
//   - 剧 2「长夜灯塔」是电影，没有海报；季 3，集 4。
func catalogItems() []catalog.Item {
	starSea := tv("星海旅人", nil, season(1, "", episode(1, "", 30), episode(2, "", 30)), season(2, "", episode(1, "", 30)))
	starSea.Poster = &catalog.Image{ContentType: "image/png", Data: []byte("星海旅人的海报")}
	return []catalog.Item{item(starSea), item(movie("长夜灯塔", nil, 5400))}
}

// seedBindings 集 1 绑定两个弹幕源（2 条、1 条弹幕），集 3、集 4 各绑定一个（1 条弹幕）。
func seedBindings(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO bindings (episode_id, adapter, ref, title, duration, danmaku_count) VALUES
			(1, 'fake', '{"name": "a"}', 'a', 30, 2),
			(1, 'fake', '{"name": "b"}', 'b', 30, 1),
			(3, 'fake', '{"name": "c"}', 'c', 30, 1),
			(4, 'fake', '{"name": "d"}', 'd', 5400, 1);
		INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text)
		SELECT b.id, n, 0, 1, 0, '弹幕'
		FROM bindings b, generate_series(1, b.danmaku_count) AS n;`)
	if err != nil {
		t.Fatal(err)
	}
}

// readContents 目录里的每一集连同它的绑定数和弹幕条数，例如"星海旅人 S1E2 绑定 1 弹幕 2"，按剧的 ID、季号、集号排序。
func readContents(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(t.Context(), `
		SELECT format('%s S%sE%s 绑定 %s 弹幕 %s', s.title, se.number, e.number, count(DISTINCT b.id), count(d.source_id))
		FROM series s
		JOIN seasons se ON se.series_id = s.id
		JOIN episodes e ON e.season_id = se.id
		LEFT JOIN bindings b ON b.episode_id = e.id
		LEFT JOIN danmaku d ON d.binding_id = b.id
		GROUP BY s.id, se.id, e.id
		ORDER BY s.id, se.number, e.number`)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

// readImages images 表里每张图片的内容，按 ID 排序。
func readImages(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	var images []string
	err := pool.QueryRow(t.Context(), `SELECT coalesce(array_agg(convert_from(data, 'UTF8') ORDER BY id), '{}') FROM images`).Scan(&images)
	if err != nil {
		t.Fatal(err)
	}
	return images
}

func newCatalogService(pool *pgxpool.Pool) *CatalogService {
	return NewCatalogService(repository.NewStore(pool), source.NewRegistry())
}

// TestDeleteCatalog 删除剧、季、集：下级的季、集、绑定和弹幕随之删除，别的剧、季、集不受影响；删除剧时它的海报一并删除。
// 删掉之后再删一次返回 404。
func TestDeleteCatalog(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		del         func(*CatalogService, context.Context, int64) error
		id          int64
		want        []string // 删除之后目录里的集
		wantImages  []string
		wantMessage string // 再删一次时 404 的提示
	}{
		{
			name: "集：它的绑定和弹幕一起删除",
			del:  (*CatalogService).DeleteEpisode, id: 1,
			want:       []string{"星海旅人 S1E2 绑定 0 弹幕 0", "星海旅人 S2E1 绑定 1 弹幕 1", "长夜灯塔 S1E1 绑定 1 弹幕 1"},
			wantImages: []string{"星海旅人的海报"}, wantMessage: "集不存在",
		},
		{
			name: "季：它的集、绑定和弹幕一起删除",
			del:  (*CatalogService).DeleteSeason, id: 1,
			want:       []string{"星海旅人 S2E1 绑定 1 弹幕 1", "长夜灯塔 S1E1 绑定 1 弹幕 1"},
			wantImages: []string{"星海旅人的海报"}, wantMessage: "季不存在",
		},
		{
			name: "剧：它的季、集、绑定、弹幕和海报一起删除",
			del:  (*CatalogService).DeleteSeries, id: 1,
			want:       []string{"长夜灯塔 S1E1 绑定 1 弹幕 1"},
			wantImages: []string{}, wantMessage: "剧不存在",
		},
		{
			name: "没有海报的剧",
			del:  (*CatalogService).DeleteSeries, id: 2,
			want:       []string{"星海旅人 S1E1 绑定 2 弹幕 3", "星海旅人 S1E2 绑定 0 弹幕 0", "星海旅人 S2E1 绑定 1 弹幕 1"},
			wantImages: []string{"星海旅人的海报"}, wantMessage: "剧不存在",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				syncOnce(t, newTestService(t, pool, &fakeSource{items: catalogItems()}))
				seedBindings(t, pool)
				svc := newCatalogService(pool)

				if err := tt.del(svc, t.Context(), tt.id); err != nil {
					t.Fatalf("删除：%v", err)
				}

				if got := readContents(t, pool); !slices.Equal(got, tt.want) {
					t.Errorf("目录 = %q\nwant %q", got, tt.want)
				}
				if got := readImages(t, pool); !slices.Equal(got, tt.wantImages) {
					t.Errorf("图片 = %q, want %q", got, tt.wantImages)
				}
				assertAppError(t, tt.del(svc, t.Context(), tt.id), http.StatusNotFound, tt.wantMessage)
			})
		})
	}
}

// TestDeleteDuringSync 同步进行中删除它还没处理到的剧、季、集：同步照常成功，删掉的部分用新 ID 重新建出来，计入这次同步的新增。
func TestDeleteDuringSync(t *testing.T) {
	t.Parallel()
	// 目录里的集（readCatalog 的格式）
	const (
		e11 = "tv|星海旅人|-|- / 1|- / 1|-|30"
		e12 = "tv|星海旅人|-|- / 1|- / 2|-|30"
		e21 = "tv|星海旅人|-|- / 2|- / 1|-|30"
	)
	tests := []struct {
		name        string
		del         func(*CatalogService, context.Context, int64) error
		id          int64
		wantCounts  string
		wantRenewed []string // 换了新 ID 的集
		wantPoster  bool     // 海报换了新图：随剧删掉，又随剧建回来
	}{
		{"剧", (*CatalogService).DeleteSeries, 1, "succeeded 2/2 新增剧 1 季 2 集 3", []string{e11, e12, e21}, true},
		{"季", (*CatalogService).DeleteSeason, 1, "succeeded 2/2 新增剧 0 季 1 集 2", []string{e11, e12}, false},
		{"集", (*CatalogService).DeleteEpisode, 1, "succeeded 2/2 新增剧 0 季 0 集 1", []string{e11}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				src := &fakeSource{items: catalogItems()}
				svc := newTestService(t, pool, src)
				syncOnce(t, svc)
				before := readCatalog(t, pool)
				posterBefore := readPoster(t, pool, "星海旅人")

				src.gate = make(chan struct{})
				runID := triggerSync(t, svc) // 同步已经开始，停在第一部剧「星海旅人」之前
				if err := tt.del(newCatalogService(pool), t.Context(), tt.id); err != nil {
					t.Fatalf("删除：%v", err)
				}
				for range src.items {
					src.gate <- struct{}{}
				}
				synctest.Wait()

				if got := runCounts(getRun(t, svc, runID)); got != tt.wantCounts {
					t.Errorf("%s, want %s", got, tt.wantCounts)
				}
				after := readCatalog(t, pool)
				if got, want := slices.Sorted(slices.Values(texts(after))), slices.Sorted(slices.Values(texts(before))); !slices.Equal(got, want) {
					t.Errorf("目录 = %q\nwant 与删除前相同 %q", got, want)
				}
				ids := make(map[string][3]int64, len(before))
				for _, r := range before {
					ids[r.text] = r.ids
				}
				var renewed []string
				for _, r := range after {
					if r.ids != ids[r.text] {
						renewed = append(renewed, r.text)
					}
				}
				slices.Sort(renewed)
				if !slices.Equal(renewed, tt.wantRenewed) {
					t.Errorf("换了新 ID 的集 = %q\nwant %q", renewed, tt.wantRenewed)
				}
				poster := readPoster(t, pool, "星海旅人")
				if poster.text != posterBefore.text {
					t.Errorf("海报 = %q, want %q", poster.text, posterBefore.text)
				}
				if newPoster := poster.id != posterBefore.id; newPoster != tt.wantPoster {
					t.Errorf("海报的图片 ID %d → %d，want 换了新图 = %v", posterBefore.id, poster.id, tt.wantPoster)
				}
			})
		})
	}
}
