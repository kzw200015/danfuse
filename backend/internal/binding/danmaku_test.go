package binding_test

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/binding"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/source"
	"github.com/kzw200015/danfuse/backend/internal/testenv"
)

// TestListDanmaku 按游标翻完一个绑定的全部弹幕：每页 200 条，按时间升序、同一时间按原始 ID，不重不漏，最后一页没有游标；
// 跳转时从那个时间开始翻。
func TestListDanmaku(t *testing.T) {
	t.Parallel()
	// 401 条，原始 ID 越大时间越早，每两条同一时间：时间顺序与原始 ID 顺序相反
	const n = 401
	items := make([]danmaku.Danmaku, n)
	for i := range items {
		id := int64(i + 1)
		items[i] = danmaku.Danmaku{SourceID: id, TimeMs: int32((n - id) / 2 * 1000), Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "弹幕"}
	}
	adapter := &fakeAdapter{sources: map[string]source.Fetched{"many": {Title: "很多弹幕", Duration: 600, Danmaku: items}}}
	svc, _ := newBindingService(t, adapter, testenv.Logger(t))
	b, err := svc.Create(t.Context(), 1, "fake/many")
	if err != nil {
		t.Fatal(err)
	}
	if b.MaxTimeMs != 200_000 {
		t.Errorf("MaxTimeMs = %d, want 200000", b.MaxTimeMs)
	}

	slices.SortFunc(items, func(a, b danmaku.Danmaku) int {
		return cmp.Or(cmp.Compare(a.TimeMs, b.TimeMs), cmp.Compare(a.SourceID, b.SourceID))
	})
	views := make([]binding.DanmakuView, n)
	for i, d := range items {
		views[i] = binding.DanmakuView{TimeMs: d.TimeMs, Mode: int16(d.Mode), Color: int32(d.Color), Text: d.Text}
	}

	got, sizes := listAllDanmaku(t, svc, b.ID, nil)
	if !slices.Equal(sizes, []int{200, 200, 1}) {
		t.Errorf("每页条数 = %v, want [200 200 1]", sizes)
	}
	if !slices.Equal(got, views) {
		t.Errorf("翻完的弹幕与按时间排序的不一致：got %d 条", len(got))
	}

	// 跳到 100 秒：从这个时间的第一条开始，翻页时照传
	from := int32(100_000)
	got, sizes = listAllDanmaku(t, svc, b.ID, &from)
	start := slices.IndexFunc(views, func(v binding.DanmakuView) bool { return v.TimeMs >= from })
	if !slices.Equal(sizes, []int{200, 1}) {
		t.Errorf("跳转后每页条数 = %v, want [200 1]", sizes)
	}
	if !slices.Equal(got, views[start:]) {
		t.Errorf("跳转后翻完的弹幕与 100 秒及以后的不一致：got %d 条", len(got))
	}
}

// listAllDanmaku 按游标翻完一个绑定的弹幕，返回全部弹幕和每页的条数。
func listAllDanmaku(t *testing.T, svc *binding.Service, id int64, fromMs *int32) ([]binding.DanmakuView, []int) {
	t.Helper()
	var got []binding.DanmakuView
	var sizes []int
	after := ""
	for {
		page, err := svc.ListDanmaku(t.Context(), id, fromMs, after)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page.Items...)
		sizes = append(sizes, len(page.Items))
		if page.Next == nil {
			return got, sizes
		}
		after = *page.Next
	}
}

func TestListDanmakuErrors(t *testing.T) {
	t.Parallel()
	svc, _ := newBindingService(t, &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}, testenv.Logger(t))
	id := createS1(t, svc)

	_, err := svc.ListDanmaku(t.Context(), 99, nil, "")
	testenv.AssertAppError(t, err, http.StatusNotFound, "绑定不存在")
	for _, cursor := range []string{"abc", "1_", "_1", "99999999999_1"} {
		_, err := svc.ListDanmaku(t.Context(), id, nil, cursor)
		testenv.AssertAppError(t, err, http.StatusBadRequest, "游标不合法")
	}
}

// platformAdapter 只回答 ID 和平台的假适配器：读取弹幕只用到这两项，调用其他方法会 panic。
type platformAdapter struct {
	source.Adapter
	id       string
	platform danmaku.Platform
}

func (a platformAdapter) ID() string                 { return a.id }
func (a platformAdapter) Platform() danmaku.Platform { return a.platform }

// newDanmakuService 新库里写入两集（testenv.SeedEpisodes）和 sql 里的绑定与弹幕，构造 binding.Service。
// 注册的适配器：bilibili 在 B 站平台，fake 没有平台。
func newDanmakuService(t *testing.T, sql string) *binding.Service {
	t.Helper()
	pool := dbtest.Pool(t)
	testenv.SeedEpisodes(t, pool)
	if _, err := pool.Exec(t.Context(), sql); err != nil {
		t.Fatal(err)
	}
	sources := source.NewRegistry(
		platformAdapter{id: "bilibili", platform: danmaku.PlatformBilibili},
		platformAdapter{id: "fake", platform: danmaku.PlatformNone},
	)
	return binding.NewService(pool, sources, testenv.Logger(t))
}

// TestEpisodeDanmaku 一集合并后的弹幕：弹弹 API 的 comment 取的就是它。
func TestEpisodeDanmaku(t *testing.T) {
	t.Parallel()
	svc := newDanmakuService(t, `
		INSERT INTO bindings (episode_id, adapter, ref, title, duration, "offset", scale, status, danmaku_count) VALUES
			(1, 'bilibili', '{"aid": 1}', 'B 站投稿', 1420, 0, 1, 'active', 3),               -- 绑定 1
			(1, 'bilibili', '{"epId": 2}', 'B 站番剧', 1422, 10, 1, 'dead', 3),               -- 绑定 2：失效
			(1, 'fake', '{"name": "local"}', '没有平台的弹幕源', 2840, -2, 0.5, 'active', 2); -- 绑定 3
		INSERT INTO bindings (episode_id, kind, title, danmaku_count) VALUES (1, 'file', '弹幕文件', 1); -- 绑定 4
		INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text) VALUES
			(1, 101, 1000, 1, 16777215, '前排'),
			(1, 102, 61000, 6, 15138834, '逆向'),
			(1, 103, 120000, 4, 0, '底部'),
			(2, 101, 1000, 1, 16777215, '前排'), -- 原始 ID 与绑定 1 的那条相同；校正后相差 10 秒，只靠按 ID 去掉
			(2, 201, 51500, 1, 0, '逆向'),       -- 校正后 61.5 秒，与绑定 1 的那条相差 0.5 秒，按文本去掉
			(2, 202, 300000, 5, 255, '失效绑定的弹幕'),
			(3, 1, 2000, 1, 0, '太早了'),        -- 2 × 0.5 − 2 = −1 秒
			(3, 2, 10000, 1, 0, '没有平台'),
			(4, 103, 200000, 1, 0, '弹幕文件里的');  -- 原始 ID 与绑定 1 的那条相同，但弹幕文件不属于任何平台，不按 ID 去掉`)

	const bili = danmaku.PlatformBilibili
	tests := []struct {
		name      string
		episodeID int64
		want      []danmaku.Item
	}{
		{
			"多个绑定按 offset 与 scale 校正、跨源去重后按时间合并；失效绑定的弹幕照常输出，弹幕文件的弹幕不属于任何平台", 1, []danmaku.Item{
				{TimeMs: 1000, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "前排", SourceID: 101, Platform: bili},
				{TimeMs: 3000, Mode: danmaku.ModeScroll, Text: "没有平台", SourceID: 2, Platform: danmaku.PlatformNone},
				{TimeMs: 61000, Mode: danmaku.ModeReverse, Color: 0xE70012, Text: "逆向", SourceID: 102, Platform: bili},
				{TimeMs: 120000, Mode: danmaku.ModeBottom, Text: "底部", SourceID: 103, Platform: bili},
				{TimeMs: 200000, Mode: danmaku.ModeScroll, Text: "弹幕文件里的", SourceID: 103, Platform: danmaku.PlatformNone},
				{TimeMs: 310000, Mode: danmaku.ModeTop, Color: 0xFF, Text: "失效绑定的弹幕", SourceID: 202, Platform: bili},
			},
		},
		{"没有绑定", 2, nil},
		{"集不存在", 99, nil},
	}
	for _, tt := range tests {
		got, err := svc.EpisodeDanmaku(t.Context(), tt.episodeID)
		if err != nil {
			t.Fatalf("%s：EpisodeDanmaku(%d): %v", tt.name, tt.episodeID, err)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s：EpisodeDanmaku(%d) = %+v\nwant %+v", tt.name, tt.episodeID, got, tt.want)
		}
	}
}

// TestEpisodeDanmakuUnknownAdapter 绑定的适配器没有注册：不知道弹幕在哪个平台，跨源去重和 cid 都无从谈起，按服务器内部错误返回。
func TestEpisodeDanmakuUnknownAdapter(t *testing.T) {
	t.Parallel()
	svc := newDanmakuService(t, `
		INSERT INTO bindings (episode_id, adapter, ref, title, duration) VALUES
			(1, 'bilibili', '{"aid": 1}', 'B 站投稿', 1420),
			(1, 'gone', '{"id": 1}', '没有注册的适配器', 1420);`)

	if got, err := svc.EpisodeDanmaku(t.Context(), 1); err == nil || !strings.Contains(err.Error(), `"gone"`) {
		t.Errorf("EpisodeDanmaku() = %+v, %v；want 指出没有注册的适配器 gone 的错误", got, err)
	}
}
