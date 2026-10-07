package service

import (
	"cmp"
	"net/http"
	"slices"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
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
	svc, _ := newBindingService(t, adapter, testLogger(t))
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
	views := make([]DanmakuView, n)
	for i, d := range items {
		views[i] = DanmakuView{TimeMs: d.TimeMs, Mode: int16(d.Mode), Color: int32(d.Color), Text: d.Text}
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
	start := slices.IndexFunc(views, func(v DanmakuView) bool { return v.TimeMs >= from })
	if !slices.Equal(sizes, []int{200, 1}) {
		t.Errorf("跳转后每页条数 = %v, want [200 1]", sizes)
	}
	if !slices.Equal(got, views[start:]) {
		t.Errorf("跳转后翻完的弹幕与 100 秒及以后的不一致：got %d 条", len(got))
	}
}

// listAllDanmaku 按游标翻完一个绑定的弹幕，返回全部弹幕和每页的条数。
func listAllDanmaku(t *testing.T, svc *BindingService, id int64, fromMs *int32) ([]DanmakuView, []int) {
	t.Helper()
	var got []DanmakuView
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
	svc, _ := newBindingService(t, &fakeAdapter{sources: map[string]source.Fetched{"s1": video}}, testLogger(t))
	id := createS1(t, svc)

	_, err := svc.ListDanmaku(t.Context(), 99, nil, "")
	assertAppError(t, err, http.StatusNotFound, "绑定不存在")
	for _, cursor := range []string{"abc", "1_", "_1", "99999999999_1"} {
		_, err := svc.ListDanmaku(t.Context(), id, nil, cursor)
		assertAppError(t, err, http.StatusBadRequest, "游标不合法")
	}
}
