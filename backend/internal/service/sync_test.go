package service

import (
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/pkg/errcode"
	"github.com/kzw200015/danfuse/backend/internal/provider"
	"github.com/kzw200015/danfuse/backend/internal/repository"
)

func TestSyncTwiceKeepsIDs(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCatalog{items: []catalog.Item{
			item(tv("星海旅人", new(2019),
				season(0, "Specials", episode(1, "特别篇", 20)),
				season(1, "第 1 季", episode(1, "第1集", 1440), catalog.Episode{Number: 2}),
			)),
			item(movie("长夜灯塔", new(2020), 5400)),
		}}
		svc := newTestService(t, pool, src)

		first := syncOnce(t, svc)
		if got, want := runCounts(first), "succeeded 2/2 新增剧 2 季 3 集 4"; got != want {
			t.Errorf("第一次同步：%s, want %s", got, want)
		}
		before := readCatalog(t, pool)
		want := []string{
			"tv|星海旅人|-|2019 / 0|Specials / 1|特别篇|20",
			"tv|星海旅人|-|2019 / 1|第 1 季 / 1|第1集|1440",
			"tv|星海旅人|-|2019 / 1|第 1 季 / 2|-|-", // 空标题、没有时长存为 null
			"movie|长夜灯塔|-|2020 / 1|- / 1|-|5400",
		}
		if got := texts(before); !slices.Equal(got, want) {
			t.Errorf("目录 = %q\nwant %q", got, want)
		}

		second := syncOnce(t, svc)
		if got, want := runCounts(second), "succeeded 2/2 新增剧 0 季 0 集 0"; got != want {
			t.Errorf("第二次同步：%s, want %s", got, want)
		}
		if after := readCatalog(t, pool); !reflect.DeepEqual(after, before) {
			t.Errorf("重复同步后目录变了\nbefore %v\nafter  %v", before, after)
		}
	})
}

func TestSyncMatchesNaturalKeys(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCatalog{}
		svc := newTestService(t, pool, src)

		starSea := tv("星海旅人", new(2019), season(1, "第 1 季", episode(1, "旧标题", 100)))
		starSea.OriginalTitle = "Star Sea"
		src.items = []catalog.Item{item(starSea), item(tv("无年份", nil, season(1, "", episode(1, "第1集", 30))))}
		syncOnce(t, svc)
		original := readCatalog(t, pool)

		// 键以外的字段用目录源的数据覆盖；年份都为空的同名同类型剧是同一部
		starSea = tv("星海旅人", new(2019), season(1, "第一季", episode(1, "新标题", 101)))
		starSea.OriginalTitle = "Star Sea 2"
		src.items = []catalog.Item{item(starSea), item(tv("无年份", nil, season(1, "", episode(1, "改过的标题", 30))))}
		run := syncOnce(t, svc)
		if got, want := runCounts(run), "succeeded 2/2 新增剧 0 季 0 集 0"; got != want {
			t.Errorf("覆盖字段：%s, want %s", got, want)
		}
		updated := readCatalog(t, pool)
		wantTexts := []string{
			"tv|星海旅人|Star Sea 2|2019 / 1|第一季 / 1|新标题|101",
			"tv|无年份|-|- / 1|- / 1|改过的标题|30",
		}
		if got := texts(updated); !slices.Equal(got, wantTexts) {
			t.Errorf("目录 = %q\nwant %q", got, wantTexts)
		}
		for i := range updated {
			if updated[i].ids != original[i].ids {
				t.Errorf("%s 的 ID 变了：%v → %v", updated[i].text, original[i].ids, updated[i].ids)
			}
		}

		// 年份或标题变化：新增一部剧，旧剧连同绑定保留。给旧剧的那一集（集 1）绑定一个弹幕源
		_, err := pool.Exec(t.Context(), `
			INSERT INTO bindings (episode_id, adapter, ref, title, duration, danmaku_count) VALUES (1, 'fake', '{"name": "a"}', 'a', 101, 2);
			INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text) VALUES (1, 1, 0, 1, 0, '前排'), (1, 2, 1500, 1, 0, '来了');`)
		if err != nil {
			t.Fatal(err)
		}
		src.items = []catalog.Item{
			item(tv("星海旅人", new(2020), season(1, "第 1 季", episode(1, "新标题", 101)))),
			item(tv("改了名", nil, season(1, "", episode(1, "第1集", 30)))),
		}
		run = syncOnce(t, svc)
		if got, want := runCounts(run), "succeeded 2/2 新增剧 2 季 2 集 2"; got != want {
			t.Errorf("年份或标题变化：%s, want %s", got, want)
		}
		wantTexts = append(wantTexts,
			"tv|星海旅人|-|2020 / 1|第 1 季 / 1|新标题|101",
			"tv|改了名|-|- / 1|- / 1|第1集|30",
		)
		if got := texts(readCatalog(t, pool)); !slices.Equal(got, wantTexts) {
			t.Errorf("目录 = %q\nwant %q", got, wantTexts)
		}
		wantContents := []string{
			"星海旅人 S1E1 绑定 1 弹幕 2", // 旧剧
			"无年份 S1E1 绑定 0 弹幕 0",
			"星海旅人 S1E1 绑定 0 弹幕 0", // 新增的剧
			"改了名 S1E1 绑定 0 弹幕 0",
		}
		if got := readContents(t, pool); !slices.Equal(got, wantContents) {
			t.Errorf("各集的绑定 = %q\nwant %q", got, wantContents)
		}
	})
}

func TestSyncDuplicateKeysLastWins(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		// 同一部剧分在两个文件夹、同一集有两个版本、多集文件与单集文件重叠：都按普通 upsert，后写的覆盖先写的
		src := &fakeCatalog{items: []catalog.Item{
			item(tv("星海旅人", new(2019), season(1, "第 1 季", episode(1, "1080p", 34), episode(1, "720p", 33)))),
			item(tv("星海旅人", new(2019),
				season(1, "Season 1", episode(2, "S01E01-E02", 50)),
				season(2, "第 2 季", episode(1, "S02E01", 30)),
			)),
			item(tv("星海旅人", new(2019), season(1, "Season 1", episode(1, "S01E01-E02", 50)))),
		}}
		svc := newTestService(t, pool, src)
		want := []string{
			"tv|星海旅人|-|2019 / 1|Season 1 / 1|S01E01-E02|50",
			"tv|星海旅人|-|2019 / 1|Season 1 / 2|S01E01-E02|50",
			"tv|星海旅人|-|2019 / 2|第 2 季 / 1|S02E01|30",
		}

		for i, wantCounts := range []string{"succeeded 3/3 新增剧 1 季 2 集 3", "succeeded 3/3 新增剧 0 季 0 集 0"} {
			run := syncOnce(t, svc)
			if got := runCounts(run); got != wantCounts {
				t.Errorf("第 %d 次同步：%s, want %s", i+1, got, wantCounts)
			}
			if got := texts(readCatalog(t, pool)); !slices.Equal(got, want) {
				t.Errorf("第 %d 次同步后目录 = %q\nwant %q", i+1, got, want)
			}
		}
	})
}

func TestSyncRecomputesSearchVectors(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCatalog{}
		svc := newTestService(t, pool, src)
		starSea := tv("星海旅人", new(2019), season(1, "", episode(1, "", 1440)), season(2, "归航篇", episode(1, "", 1440)))
		starSea.OriginalTitle = "Star Voyager"
		src.items = []catalog.Item{item(starSea)}
		syncOnce(t, svc)

		// 原名变了，这次目录源只给出第 1 季：第 2 季的搜索列也要按新原名重算
		starSea = tv("星海旅人", new(2019), season(1, "", episode(1, "", 1440)))
		starSea.OriginalTitle = "Star Traveler"
		src.items = []catalog.Item{item(starSea)}
		syncOnce(t, svc)

		bothSeasons := []string{"星海旅人 · 剧集 2019 · 共 1 集 · 1", "星海旅人 第2季 · 剧集 2019 · 共 1 集 · 1"}
		for keyword, want := range map[string][]string{
			"Star Traveler": bothSeasons,
			"Star Voyager":  nil,
			"归航":            {"星海旅人 第2季 · 剧集 2019 · 共 1 集 · 1"}, // 这次没有给出的季保留原来的季标题
		} {
			if got, _ := searchSeasons(t, pool, provider.SearchQuery{Keyword: keyword, MaxSeasons: 50}); !slices.Equal(got, want) {
				t.Errorf("Search(%q) = %q, want %q", keyword, got, want)
			}
		}
	})
}

func TestSyncPoster(t *testing.T) {
	t.Parallel()
	image := func(contentType, data string) *catalog.Image {
		return &catalog.Image{ContentType: contentType, Data: []byte(data)}
	}
	text := func(img *catalog.Image) string { // 与 readPoster 的 text 同样格式
		if img == nil {
			return ""
		}
		return img.ContentType + "|" + string(img.Data)
	}
	errDownload := errors.New("GET /Items/x/Images/Primary: 500 Internal Server Error")
	tests := []struct {
		name         string
		before       *catalog.Image // 第一次同步时目录源给的海报
		poster       *catalog.Image // 第二次同步时目录源给的海报，与 posterErr 都为 nil 表示没有图
		posterErr    error
		wantText     string // 第二次同步后剧的海报："content-type|内容"，没有海报时为空
		wantSameID   bool   // 图片 ID 与第一次同步后相同
		wantWarnings []string
	}{
		{
			name:       "没变：不写新图",
			before:     image("image/png", "海报"),
			poster:     image("image/png", "海报"),
			wantText:   "image/png|海报",
			wantSameID: true,
		},
		{
			name:     "变了：换图并删除旧图", // content-type 不变，只有内容变了：按 sha256 比较
			before:   image("image/png", "旧海报"),
			poster:   image("image/png", "新海报"),
			wantText: "image/png|新海报",
		},
		{
			name:     "原来没有海报：写入新图",
			poster:   image("image/png", "海报"),
			wantText: "image/png|海报",
		},
		{
			name:   "没有图：清空海报并删除旧图",
			before: image("image/png", "海报"),
		},
		{
			name:         "下载失败：保留旧图并记警告",
			before:       image("image/png", "海报"),
			posterErr:    errDownload,
			wantText:     "image/png|海报",
			wantSameID:   true,
			wantWarnings: []string{"甲：下载海报失败：GET /Items/x/Images/Primary: 500 Internal Server Error"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				series := tv("甲", nil, season(1, "", episode(1, "", 30)))
				series.Poster = tt.before
				src := &fakeCatalog{items: []catalog.Item{item(series)}}
				svc := newTestService(t, pool, src)
				syncOnce(t, svc)
				before := readPoster(t, pool, "甲")
				if before.text != text(tt.before) {
					t.Fatalf("第一次同步后海报 = %q, want %q", before.text, text(tt.before))
				}

				series = tv("甲", nil, season(1, "", episode(1, "", 30)))
				series.Poster, series.PosterErr = tt.poster, tt.posterErr
				src.items = []catalog.Item{item(series)}
				run := syncOnce(t, svc)

				if run.Status != statusSucceeded {
					t.Errorf("海报只影响这部剧，同步应成功，实际 %s：%v", run.Status, run.Error)
				}
				after := readPoster(t, pool, "甲")
				if after.text != tt.wantText {
					t.Errorf("海报 = %q, want %q", after.text, tt.wantText)
				}
				if sameID := after.id == before.id; sameID != tt.wantSameID {
					t.Errorf("图片 ID %d → %d，want 相同 = %v", before.id, after.id, tt.wantSameID)
				}
				if !slices.Equal(run.Warnings, tt.wantWarnings) {
					t.Errorf("警告 = %q, want %q", run.Warnings, tt.wantWarnings)
				}
			})
		})
	}
}

func TestSyncSkipsInvalidSeries(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCatalog{
			warnings: []string{"找不到媒体库「动画」，已跳过"},
			items: []catalog.Item{
				{Name: "雾港谜案", Warnings: []string{"集「雾港谜案」（第1季，第 1 集）没有季号，已跳过", "没有有效的集，整部跳过"}},
				{Name: "标题空白", Series: tv(" ", nil, season(1, "", episode(1, "", 30)))},
				item(movie("长夜灯塔", nil, 5400), "集号范围异常"),
				{Name: "两集的电影", Series: &catalog.Series{Type: catalog.TypeMovie, Title: "两集的电影", Seasons: []catalog.Season{
					season(1, "", episode(1, "", 30), episode(2, "", 30)),
				}}},
			},
		}
		svc := newTestService(t, pool, src)

		run := syncOnce(t, svc)

		if got, want := runCounts(run), "succeeded 4/4 新增剧 1 季 1 集 1"; got != want {
			t.Errorf("%s, want %s（整部被跳过的剧也计入进度）", got, want)
		}
		wantWarnings := []string{
			"找不到媒体库「动画」，已跳过",
			"雾港谜案：集「雾港谜案」（第1季，第 1 集）没有季号，已跳过",
			"雾港谜案：没有有效的集，整部跳过",
			"标题空白：标题为空，整部跳过",
			"长夜灯塔：集号范围异常",
			"两集的电影：电影只能有第 1 季第 1 集，整部跳过",
		}
		if !slices.Equal(run.Warnings, wantWarnings) || run.WarningCount != int32(len(wantWarnings)) {
			t.Errorf("警告 = %q（共 %d 条）\nwant %q", run.Warnings, run.WarningCount, wantWarnings)
		}
		if got, want := texts(readCatalog(t, pool)), []string{"movie|长夜灯塔|-|- / 1|- / 1|-|5400"}; !slices.Equal(got, want) {
			t.Errorf("目录 = %q, want %q", got, want)
		}
	})
}

func TestSyncKeepsFirst200Warnings(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		var warnings []string
		for i := range 150 {
			warnings = append(warnings, fmt.Sprintf("警告 %d", i))
		}
		src := &fakeCatalog{
			warnings: warnings[:50],
			items:    []catalog.Item{{Name: "甲", Warnings: warnings[50:]}, {Name: "乙", Warnings: warnings[50:]}},
		}
		svc := newTestService(t, pool, src)

		run := syncOnce(t, svc)

		if run.WarningCount != 250 {
			t.Errorf("警告总数 = %d, want 250", run.WarningCount)
		}
		if len(run.Warnings) != 200 || run.Warnings[0] != "警告 0" || run.Warnings[199] != "乙：警告 99" {
			t.Errorf("应只保存前 200 条，实际 %d 条，首尾为 %q、%q", len(run.Warnings), run.Warnings[0], run.Warnings[len(run.Warnings)-1])
		}
	})
}

func TestSyncKeepsLatest20Runs(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		svc := newTestService(t, pool, &fakeCatalog{})
		for range 25 {
			syncOnce(t, svc)
		}

		runs, err := svc.ListRuns(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, r := range runs {
			ids = append(ids, r.ID)
		}
		want := []int64{25, 24, 23, 22, 21, 20, 19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6}
		if !slices.Equal(ids, want) {
			t.Errorf("同步记录 = %v, want %v", ids, want)
		}
		if _, err := svc.GetRun(t.Context(), 5); !errors.Is(err, errcode.ErrNotFound) {
			t.Errorf("第 20 次以前的记录应被删掉，GetRun(5) error = %v", err)
		}
	})
}

func TestSyncFailures(t *testing.T) {
	t.Parallel()
	items := []catalog.Item{
		item(tv("甲", nil, season(1, "", episode(1, "", 30)))),
		item(tv("乙", nil, season(1, "", episode(1, "", 200)))),
		item(tv("丙", nil, season(1, "", episode(1, "", 30)))),
	}
	tests := []struct {
		name       string
		src        *fakeCatalog
		setup      string // 同步前执行的 SQL
		wantCounts string
		wantError  string
		wantSeries []string // 失败之前已提交的剧
	}{
		{
			name:       "列清单失败",
			src:        &fakeCatalog{listErr: errors.New("配置的媒体库都不可用：找不到媒体库「番剧」，已跳过")},
			wantCounts: "failed -/0 新增剧 0 季 0 集 0",
			wantError:  "配置的媒体库都不可用：找不到媒体库「番剧」，已跳过",
		},
		{
			name:       "中途请求失败",
			src:        &fakeCatalog{items: items, failAt: 3},
			wantCounts: "failed 3/2 新增剧 2 季 2 集 2",
			wantError:  "目录源请求失败",
			wantSeries: []string{"甲", "乙"},
		},
		{
			name:       "数据库出错",
			src:        &fakeCatalog{items: items},
			setup:      "ALTER TABLE episodes ADD CONSTRAINT short_episode CHECK (duration < 100)",
			wantCounts: "failed 3/1 新增剧 1 季 1 集 1",
			wantError:  "写入「乙」失败：",
			wantSeries: []string{"甲"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
				if tt.setup != "" {
					if _, err := pool.Exec(t.Context(), tt.setup); err != nil {
						t.Fatal(err)
					}
				}
				svc := newTestService(t, pool, tt.src)

				run := syncOnce(t, svc)

				if got := runCounts(run); got != tt.wantCounts {
					t.Errorf("%s, want %s", got, tt.wantCounts)
				}
				if run.Error == nil || !strings.HasPrefix(*run.Error, tt.wantError) {
					t.Errorf("失败原因 = %v, want %q", run.Error, tt.wantError)
				}
				if run.FinishedAt == nil {
					t.Error("失败的同步应有结束时间")
				}
				var series []string
				if err := pool.QueryRow(t.Context(), "SELECT coalesce(array_agg(title ORDER BY id), '{}') FROM series").Scan(&series); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(series, tt.wantSeries) {
					t.Errorf("保留的剧 = %q, want %q", series, tt.wantSeries)
				}
			})
		})
	}
}

func TestSyncProgress(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCatalog{
			items: []catalog.Item{
				item(tv("甲", nil, season(1, "", episode(1, "", 30)))),
				{Name: "乙", Warnings: []string{"没有有效的集，整部跳过"}},
				item(tv("丙", nil, season(1, "", episode(1, "", 30)))),
			},
			gate: make(chan struct{}),
		}
		svc := newTestService(t, pool, src)

		id := triggerSync(t, svc)
		skipped := []string{"乙：没有有效的集，整部跳过"}
		for i, want := range []struct {
			counts   string
			warnings []string // 警告随进度写入，进行中的详情就能看到
		}{
			{"running 3/0 新增剧 0 季 0 集 0", []string{}}, // 列完清单就有总数
			{"running 3/1 新增剧 1 季 1 集 1", []string{}}, // 每提交一部剧已完成数加一
			{"running 3/2 新增剧 1 季 1 集 1", skipped},    // 跳过的剧也算
			{"succeeded 3/3 新增剧 2 季 2 集 2", skipped},
		} {
			if i > 0 {
				src.gate <- struct{}{}
				synctest.Wait()
			}
			run := getRun(t, svc, id)
			if got := runCounts(run); got != want.counts {
				t.Errorf("放行 %d 部后：%s, want %s", i, got, want.counts)
			}
			if !slices.Equal(run.Warnings, want.warnings) || run.WarningCount != int32(len(want.warnings)) {
				t.Errorf("放行 %d 部后：警告 = %q（共 %d 条）, want %q", i, run.Warnings, run.WarningCount, want.warnings)
			}
		}
	})
}

func TestTriggerRejected(t *testing.T) {
	t.Parallel()

	t.Run("未配置目录源", func(t *testing.T) {
		t.Parallel()
		syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
			svc := newTestService(t, pool, nil)

			if _, err := svc.Trigger(t.Context()); !errors.Is(err, errNoCatalogSource) {
				t.Errorf("Trigger() error = %v, want errNoCatalogSource", err)
			}
		})
	})

	t.Run("同步正在进行", func(t *testing.T) {
		t.Parallel()
		syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
			src := &fakeCatalog{items: []catalog.Item{item(tv("甲", nil, season(1, "", episode(1, "", 30))))}, gate: make(chan struct{})}
			svc := newTestService(t, pool, src)
			first, err := svc.Trigger(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			synctest.Wait()

			if _, err := svc.Trigger(t.Context()); !errors.Is(err, errSyncRunning) {
				t.Errorf("Trigger() error = %v, want errSyncRunning", err)
			}

			src.gate <- struct{}{}
			synctest.Wait()
			if got := getRun(t, svc, first).Status; got != statusSucceeded {
				t.Errorf("第一次同步 %s, want succeeded", got)
			}
		})
	})

	t.Run("服务正在关闭", func(t *testing.T) {
		t.Parallel()
		syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
			svc := NewSyncService(repository.NewStore(pool), pool, &fakeCatalog{}, config.Sync{}, testLogger(t))
			stop := runInBackground(t, svc)
			stop() // Run 已返回：不再等它接收触发

			_, err := svc.Trigger(t.Context())
			if appErr, ok := errors.AsType[*errcode.Error](err); !ok || appErr.HTTPStatus != http.StatusServiceUnavailable || appErr.Message != "服务正在关闭" {
				t.Errorf("Trigger() error = %v, want 503 服务正在关闭", err)
			}
		})
	})

	t.Run("其他实例持有同步锁", func(t *testing.T) {
		t.Parallel()
		syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
			svc := newTestService(t, pool, &fakeCatalog{})
			unlock := holdSyncLock(t, pool)

			if _, err := svc.Trigger(t.Context()); !errors.Is(err, errSyncRunning) {
				t.Errorf("Trigger() error = %v, want errSyncRunning", err)
			}

			unlock()
			if run := syncOnce(t, svc); run.Status != statusSucceeded {
				t.Errorf("释放锁后同步 %s, want succeeded", run.Status)
			}
		})
	})
}

func TestScheduledSync(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCatalog{items: []catalog.Item{item(tv("甲", nil, season(1, "", episode(1, "", 30))))}, gate: make(chan struct{})}
		var logs lockedBuffer
		svc := NewSyncService(repository.NewStore(pool), pool, src, config.Sync{Interval: time.Hour}, slogTo(&logs))
		runInBackground(t, svc)

		statuses := func() []string {
			t.Helper()
			runs, err := svc.ListRuns(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			var result []string
			for _, r := range slices.Backward(runs) {
				result = append(result, r.Trigger+" "+r.Status)
			}
			return result
		}
		check := func(when string, want ...string) {
			t.Helper()
			synctest.Wait()
			if got := statuses(); !slices.Equal(got, want) {
				t.Errorf("%s：同步记录 = %q, want %q", when, got, want)
			}
		}

		check("启动时不立即同步")
		time.Sleep(time.Hour - time.Second)
		check("间隔未到")
		time.Sleep(time.Second)
		check("到达间隔", "schedule running")

		time.Sleep(time.Hour)
		check("有同步在跑时定时触发被跳过", "schedule running")
		if !strings.Contains(logs.String(), `level=INFO msg="sync already running, skipped" trigger=schedule`) {
			t.Errorf("跳过定时同步应记 info 日志，实际日志：\n%s", logs.String())
		}

		src.gate <- struct{}{}
		check("第一次同步结束", "schedule succeeded")
		time.Sleep(time.Hour)
		src.gate <- struct{}{}
		check("下一个间隔", "schedule succeeded", "schedule succeeded")
	})
}

func TestNoScheduleWithoutSource(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		svc := NewSyncService(repository.NewStore(pool), pool, nil, config.Sync{Interval: time.Hour}, testLogger(t))
		runInBackground(t, svc)

		time.Sleep(3 * time.Hour)
		synctest.Wait()
		if runs, err := svc.ListRuns(t.Context()); err != nil || len(runs) != 0 {
			t.Errorf("未配置目录源时不应定时同步：%v, %v", runs, err)
		}
	})
}

func TestSyncInterruptedOnShutdown(t *testing.T) {
	t.Parallel()
	syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		src := &fakeCatalog{
			items: []catalog.Item{
				item(tv("甲", nil, season(1, "", episode(1, "", 30)))),
				item(tv("乙", nil, season(1, "", episode(1, "", 30)))),
			},
			gate: make(chan struct{}),
		}
		svc := NewSyncService(repository.NewStore(pool), pool, src, config.Sync{}, testLogger(t))
		stop := runInBackground(t, svc)
		id := triggerSync(t, svc)
		src.gate <- struct{}{}
		synctest.Wait()

		stop() // 应用的 ctx 取消：Run 等这次同步写完最终状态才返回

		run := getRun(t, svc, id)
		if got, want := runCounts(run), "interrupted 2/1 新增剧 1 季 1 集 1"; got != want {
			t.Errorf("%s, want %s", got, want)
		}
		if run.FinishedAt == nil || run.Error != nil {
			t.Errorf("finishedAt = %v, error = %v, want 有结束时间、没有失败原因", run.FinishedAt, run.Error)
		}
		if got := texts(readCatalog(t, pool)); len(got) != 1 {
			t.Errorf("已提交的部分应保留，目录 = %q", got)
		}
	})
}

func TestStaleRunningRuns(t *testing.T) {
	t.Parallel()
	insertRun := func(t *testing.T, pool *pgxpool.Pool, status string) int64 {
		t.Helper()
		var id int64
		err := pool.QueryRow(t.Context(), "INSERT INTO sync_runs (trigger, status) VALUES ('manual', $1) RETURNING id", status).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	t.Run("启动时清理", func(t *testing.T) {
		t.Parallel()
		syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
			stale := insertRun(t, pool, statusRunning)
			done := insertRun(t, pool, statusSucceeded)

			svc := newTestService(t, pool, &fakeCatalog{})

			if got := getRun(t, svc, stale).Status; got != statusInterrupted {
				t.Errorf("残留的 running 应改为 interrupted，实际 %s", got)
			}
			if got := getRun(t, svc, done).Status; got != statusSucceeded {
				t.Errorf("已结束的记录不应改动，实际 %s", got)
			}
		})
	})

	t.Run("拿不到锁时不清理，新同步拿到锁后清理", func(t *testing.T) {
		t.Parallel()
		syncTest(t, func(t *testing.T, pool *pgxpool.Pool) {
			// 另一个实例正在同步：它持有锁，它的记录是 running
			unlock := holdSyncLock(t, pool)
			other := insertRun(t, pool, statusRunning)

			svc := newTestService(t, pool, &fakeCatalog{})
			if got := getRun(t, svc, other).Status; got != statusRunning {
				t.Errorf("其他实例正在进行的同步不应被清理，实际 %s", got)
			}

			// 那个实例被杀：锁随连接释放，记录残留为 running
			unlock()
			run := syncOnce(t, svc)
			if got := getRun(t, svc, other).Status; got != statusInterrupted {
				t.Errorf("新同步拿到锁后应清理残留的 running，实际 %s", got)
			}
			if run.Status != statusSucceeded {
				t.Errorf("新同步 %s, want succeeded", run.Status)
			}
		})
	})
}
