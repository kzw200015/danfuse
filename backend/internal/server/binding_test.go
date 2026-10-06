package server

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// fakeAdapter 假的源适配器：链接 "fake/<名字>" 的 ref 为 {"name":"<名字>"}。合集的方法在 season_binding_test.go。
// Fetch 对名字 gone 返回 NotFound、对 down 返回 Upstream，其余返回两条弹幕。
type fakeAdapter struct{}

var _ source.Adapter = fakeAdapter{}

type fakeRef struct {
	Name string `json:"name"`
}

func (fakeAdapter) ID() string                 { return "fake" }
func (fakeAdapter) Platform() danmaku.Platform { return danmaku.PlatformNone }

func (fakeAdapter) Describe(ref source.Ref) (source.Display, error) {
	var r fakeRef
	err := json.Unmarshal(ref, &r)
	return source.Display{URL: "https://fake.test/" + r.Name, Label: "假弹幕源 " + r.Name}, err
}

func (fakeAdapter) ParseLink(_ context.Context, link string) (source.Ref, error) {
	name, ok := strings.CutPrefix(link, "fake/")
	if !ok {
		return nil, source.ErrUnrecognized
	}
	return json.Marshal(fakeRef{Name: name})
}

func (fakeAdapter) Fetch(_ context.Context, ref source.Ref) (source.Fetched, error) {
	var r fakeRef
	if err := json.Unmarshal(ref, &r); err != nil {
		return source.Fetched{}, err
	}
	switch r.Name {
	case "gone":
		return source.Fetched{}, &source.Error{Kind: source.NotFound, Message: "视频不存在、已删除或不可见"}
	case "down":
		return source.Fetched{}, &source.Error{Kind: source.Upstream, Message: "B 站接口异常", Err: errors.New("HTTP 503")}
	}
	return source.Fetched{Title: "弹幕源 " + r.Name, Duration: 1418, Danmaku: []danmaku.Danmaku{
		{SourceID: 1, TimeMs: 0, Mode: danmaku.ModeScroll, Text: "前排"},
		{SourceID: 2, TimeMs: 1500, Mode: danmaku.ModeTop, Color: 0xFFFFFF, Text: "来了"},
	}}, nil
}

func TestCreateBinding(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	// 201 和绑定：不含 ref 和 contentVersion
	_, _, data := call(t, srv, http.MethodPost, "/api/episodes/2/bindings", `{"url": " fake/x "}`, http.StatusCreated)
	binding := decodeObject(t, data)
	wantFields := []string{
		"adapter", "danmakuCount", "duration", "id", "kind", "lastFetchedAt", "offset",
		"seasonBindingId", "sourceLabel", "sourceUrl", "status", "title",
	}
	if got := slices.Sorted(maps.Keys(binding)); !slices.Equal(got, wantFields) {
		t.Errorf("绑定的字段 = %q\nwant %q", got, wantFields)
	}
	if string(binding["lastFetchedAt"]) == "null" {
		t.Error("lastFetchedAt 为 null，want 这次拉取的时间")
	}
	delete(binding, "lastFetchedAt")
	assertJSON(t, json.RawMessage(jsonString(binding)), `{
		"id": 5, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/x", "sourceLabel": "假弹幕源 x",
		"title": "弹幕源 x", "duration": 1418, "offset": 0, "status": "active", "danmakuCount": 2, "seasonBindingId": null
	}`)

	// 剧列表随之更新
	_, _, data = call(t, srv, http.MethodGet, "/api/series", "", http.StatusOK)
	var list []map[string]json.RawMessage
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatal(err)
	}
	if got := string(list[0]["boundEpisodeCount"]) + "/" + string(list[0]["bindingCount"]); got != "3/4" {
		t.Errorf("星海旅人的已绑定集数/绑定数 = %s, want 3/4", got)
	}

	for _, tt := range []struct {
		target      string
		body        string
		wantStatus  int
		wantMessage string
	}{
		{"/api/episodes/2/bindings", `{"url": "fake/x"}`, http.StatusConflict, "这一集已经绑定过这个弹幕源"},
		{"/api/episodes/2/bindings", `{"url": "https://example.com/v/1"}`, http.StatusBadRequest, "无法识别的链接"},
		{"/api/episodes/2/bindings", `{"url": "  "}`, http.StatusBadRequest, "请粘贴弹幕源的链接"},
		{"/api/episodes/2/bindings", `{"url": `, http.StatusBadRequest, "请求参数错误"},
		{"/api/episodes/0/bindings", `{"url": "fake/x"}`, http.StatusBadRequest, "集 ID 不合法"},
		{"/api/episodes/99/bindings", `{"url": "fake/x"}`, http.StatusNotFound, "集不存在"},
		{"/api/episodes/2/bindings", `{"url": "fake/gone"}`, http.StatusUnprocessableEntity, "视频不存在、已删除或不可见"},
		{"/api/episodes/2/bindings", `{"url": "fake/down"}`, http.StatusBadGateway, "B 站接口异常"},
	} {
		if code, message, _ := call(t, srv, http.MethodPost, tt.target, tt.body, tt.wantStatus); code != 1 || message != tt.wantMessage {
			t.Errorf("POST %s %s: code=%d message=%q, want %q", tt.target, tt.body, code, message, tt.wantMessage)
		}
	}
}

func TestRefetchBinding(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO bindings (episode_id, adapter, ref, title, duration) VALUES
			(2, 'fake', '{"name": "gone"}', '弹幕源 gone', 1420), -- 绑定 5：弹幕源已不存在
			(2, 'fake', '{"name": "down"}', '弹幕源 down', 1420); -- 绑定 6：拉取时接口异常`)
	if err != nil {
		t.Fatal(err)
	}
	srv := catalogServer(pool)

	for _, tt := range []struct {
		target string
		body   string
		want   string // 不含 lastFetchedAt
	}{
		// 绑定 1 已有原始 ID 为 1、2 的两条弹幕：没有新弹幕；偏移不变。没传 clear 时为重新拉取
		{"/api/bindings/1/refetch", `{"clear": false}`, `{"added": 0, "binding": {
			"id": 1, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/a", "sourceLabel": "假弹幕源 a",
			"title": "弹幕源 a", "duration": 1418, "offset": 1.5, "status": "active", "danmakuCount": 2, "seasonBindingId": null}}`},
		{"/api/bindings/1/refetch", `{}`, `{"added": 0, "binding": {
			"id": 1, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/a", "sourceLabel": "假弹幕源 a",
			"title": "弹幕源 a", "duration": 1418, "offset": 1.5, "status": "active", "danmakuCount": 2, "seasonBindingId": null}}`},
		// 失效的绑定 2 清空后重新拉取：新增条数为总条数，恢复正常
		{"/api/bindings/2/refetch", `{"clear": true}`, `{"added": 2, "binding": {
			"id": 2, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/b", "sourceLabel": "假弹幕源 b",
			"title": "弹幕源 b", "duration": 1418, "offset": 0, "status": "active", "danmakuCount": 2, "seasonBindingId": null}}`},
	} {
		_, _, data := call(t, srv, http.MethodPost, tt.target, tt.body, http.StatusOK)
		result := decodeObject(t, data)
		binding := decodeObject(t, result["binding"])
		if string(binding["lastFetchedAt"]) == "null" {
			t.Errorf("POST %s %s: lastFetchedAt 为 null，want 这次拉取的时间", tt.target, tt.body)
		}
		delete(binding, "lastFetchedAt")
		result["binding"] = json.RawMessage(jsonString(binding))
		assertJSON(t, json.RawMessage(jsonString(result)), tt.want)
	}

	for _, tt := range []struct {
		target      string
		body        string
		wantStatus  int
		wantMessage string
	}{
		{"/api/bindings/5/refetch", `{"clear": true}`, http.StatusUnprocessableEntity, "视频不存在、已删除或不可见"},
		{"/api/bindings/6/refetch", `{"clear": true}`, http.StatusBadGateway, "B 站接口异常"},
		{"/api/bindings/99/refetch", `{}`, http.StatusNotFound, "绑定不存在"},
		{"/api/bindings/0/refetch", `{}`, http.StatusBadRequest, "绑定 ID 不合法"},
		{"/api/bindings/1/refetch", `{"clear": "yes"}`, http.StatusBadRequest, "请求参数错误"},
	} {
		if code, message, _ := call(t, srv, http.MethodPost, tt.target, tt.body, tt.wantStatus); code != 1 || message != tt.wantMessage {
			t.Errorf("POST %s %s: code=%d message=%q, want %q", tt.target, tt.body, code, message, tt.wantMessage)
		}
	}
	// 弹幕源不存在时标为失效，接口异常时状态不变
	var statuses []string
	if err := pool.QueryRow(t.Context(), `SELECT array_agg(status ORDER BY id) FROM bindings WHERE id IN (5, 6)`).Scan(&statuses); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(statuses, []string{"dead", "active"}) {
		t.Errorf("绑定 5、6 的状态 = %v, want [dead active]", statuses)
	}
}

func TestUpdateBinding(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	_, _, data := call(t, srv, http.MethodPatch, "/api/bindings/1", `{"offset": -12.5}`, http.StatusOK)
	assertJSON(t, data, `{
		"id": 1, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/a", "sourceLabel": "假弹幕源 a",
		"title": "弹幕源 a", "duration": 1440, "offset": -12.5, "status": "active", "danmakuCount": 2, "seasonBindingId": null, "lastFetchedAt": null
	}`)
	// 上下限本身是合法的
	for _, offset := range []string{"86400", "-86400"} {
		_, _, data := call(t, srv, http.MethodPatch, "/api/bindings/1", `{"offset": `+offset+`}`, http.StatusOK)
		if got := string(decodeObject(t, data)["offset"]); got != offset {
			t.Errorf("offset = %s, want %s", got, offset)
		}
	}

	for _, tt := range []struct {
		target      string
		body        string
		wantStatus  int
		wantMessage string
	}{
		{"/api/bindings/1", `{"offset": 86400.5}`, http.StatusBadRequest, "偏移必须是 -86400 到 86400 之间的秒数"},
		{"/api/bindings/1", `{"offset": -86401}`, http.StatusBadRequest, "偏移必须是 -86400 到 86400 之间的秒数"},
		{"/api/bindings/1", `{}`, http.StatusBadRequest, "偏移必须是 -86400 到 86400 之间的秒数"},
		{"/api/bindings/1", `{"offset": null}`, http.StatusBadRequest, "偏移必须是 -86400 到 86400 之间的秒数"},
		// JSON 写不出 NaN 和无穷大；超出 float64 范围的数解析失败
		{"/api/bindings/1", `{"offset": 1e999}`, http.StatusBadRequest, "请求参数错误"},
		{"/api/bindings/1", `{"offset": "1"}`, http.StatusBadRequest, "请求参数错误"},
		{"/api/bindings/0", `{"offset": 1}`, http.StatusBadRequest, "绑定 ID 不合法"},
		{"/api/bindings/99", `{"offset": 1}`, http.StatusNotFound, "绑定不存在"},
	} {
		if code, message, _ := call(t, srv, http.MethodPatch, tt.target, tt.body, tt.wantStatus); code != 1 || message != tt.wantMessage {
			t.Errorf("PATCH %s %s: code=%d message=%q, want %q", tt.target, tt.body, code, message, tt.wantMessage)
		}
	}
}

func TestDeleteBinding(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	if code, _, data := call(t, srv, http.MethodDelete, "/api/bindings/1", "", http.StatusOK); code != 0 || string(data) != "null" {
		t.Errorf("code=%d data=%s, want 0 null", code, data)
	}
	// 它的弹幕一起删除
	var rows int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM danmaku WHERE binding_id = 1`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("还留着 %d 条弹幕", rows)
	}

	for _, tt := range []struct {
		target      string
		wantStatus  int
		wantMessage string
	}{
		{"/api/bindings/1", http.StatusNotFound, "绑定不存在"},
		{"/api/bindings/0", http.StatusBadRequest, "绑定 ID 不合法"},
		{"/api/bindings/abc", http.StatusBadRequest, "请求参数错误"},
	} {
		if code, message, _ := call(t, srv, http.MethodDelete, tt.target, "", tt.wantStatus); code != 1 || message != tt.wantMessage {
			t.Errorf("DELETE %s: code=%d message=%q, want %q", tt.target, code, message, tt.wantMessage)
		}
	}
}
