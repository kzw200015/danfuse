package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	popTime(t, binding, "lastFetchedAt") // 这次拉取的时间
	assertJSON(t, json.RawMessage(jsonString(binding)), `{
		"id": 5, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/x", "sourceLabel": "假弹幕源 x",
		"title": "弹幕源 x", "duration": 1418, "offset": 0, "status": "active", "contentVersion": 1, "danmakuCount": 2, "maxTimeMs": 1500, "seasonBindingId": null
	}`)

	assertAPIErrors(t, srv, []apiError{
		{http.MethodPost, "/api/episodes/2/bindings", `{"url": "fake/x"}`, http.StatusConflict, "这一集已经绑定过这个弹幕源"},
		{http.MethodPost, "/api/episodes/2/bindings", `{"url": "https://example.com/v/1"}`, http.StatusBadRequest, "无法识别的链接"},
		{http.MethodPost, "/api/episodes/2/bindings", `{"url": "  "}`, http.StatusBadRequest, "请粘贴弹幕源的链接"},
		{http.MethodPost, "/api/episodes/2/bindings", `{"url": `, http.StatusBadRequest, "请求参数错误"},
		{http.MethodPost, "/api/episodes/0/bindings", `{"url": "fake/x"}`, http.StatusBadRequest, "集 ID 不合法"},
		{http.MethodPost, "/api/episodes/99/bindings", `{"url": "fake/x"}`, http.StatusNotFound, "集不存在"},
		{http.MethodPost, "/api/episodes/2/bindings", `{"url": "fake/gone"}`, http.StatusUnprocessableEntity, "视频不存在、已删除或不可见"},
		{http.MethodPost, "/api/episodes/2/bindings", `{"url": "fake/down"}`, http.StatusBadGateway, "B 站接口异常"},
	})
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
		// 没传 clear 时为重新拉取：绑定 1 已有原始 ID 为 1、2 的两条弹幕，没有新弹幕；偏移不变
		{"/api/bindings/1/refetch", `{}`, `{"added": 0, "binding": {
			"id": 1, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/a", "sourceLabel": "假弹幕源 a",
			"title": "弹幕源 a", "duration": 1418, "offset": 1.5, "status": "active", "contentVersion": 0, "danmakuCount": 2, "maxTimeMs": 1500, "seasonBindingId": null}}`},
		// 失效的绑定 2 清空后重新拉取：新增条数为总条数，恢复正常
		{"/api/bindings/2/refetch", `{"clear": true}`, `{"added": 2, "binding": {
			"id": 2, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/b", "sourceLabel": "假弹幕源 b",
			"title": "弹幕源 b", "duration": 1418, "offset": 0, "status": "active", "contentVersion": 1, "danmakuCount": 2, "maxTimeMs": 1500, "seasonBindingId": null}}`},
	} {
		_, _, data := call(t, srv, http.MethodPost, tt.target, tt.body, http.StatusOK)
		result := decodeObject(t, data)
		binding := decodeObject(t, result["binding"])
		popTime(t, binding, "lastFetchedAt") // 这次拉取的时间
		result["binding"] = json.RawMessage(jsonString(binding))
		assertJSON(t, json.RawMessage(jsonString(result)), tt.want)
	}

	// 弹幕源不存在时标为失效、接口异常时状态不变，见 service 的 TestRefetchDeadAndRecover
	assertAPIErrors(t, srv, []apiError{
		{http.MethodPost, "/api/bindings/5/refetch", `{"clear": true}`, http.StatusUnprocessableEntity, "视频不存在、已删除或不可见"},
		{http.MethodPost, "/api/bindings/6/refetch", `{"clear": true}`, http.StatusBadGateway, "B 站接口异常"},
		{http.MethodPost, "/api/bindings/99/refetch", `{}`, http.StatusNotFound, "绑定不存在"},
		{http.MethodPost, "/api/bindings/0/refetch", `{}`, http.StatusBadRequest, "绑定 ID 不合法"},
		{http.MethodPost, "/api/bindings/1/refetch", `{"clear": "yes"}`, http.StatusBadRequest, "请求参数错误"},
	})
}

func TestUpdateBinding(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	_, _, data := call(t, srv, http.MethodPatch, "/api/bindings/1", `{"offset": -12.5}`, http.StatusOK)
	assertJSON(t, data, `{
		"id": 1, "kind": "link", "adapter": "fake", "sourceUrl": "https://fake.test/a", "sourceLabel": "假弹幕源 a",
		"title": "弹幕源 a", "duration": 1440, "offset": -12.5, "status": "active", "contentVersion": 0, "danmakuCount": 2, "maxTimeMs": 1500, "seasonBindingId": null, "lastFetchedAt": null
	}`)
	// 上下限本身是合法的
	for _, offset := range []string{"86400", "-86400"} {
		_, _, data := call(t, srv, http.MethodPatch, "/api/bindings/1", `{"offset": `+offset+`}`, http.StatusOK)
		if got := string(decodeObject(t, data)["offset"]); got != offset {
			t.Errorf("offset = %s, want %s", got, offset)
		}
	}

	const invalidOffset = "偏移必须是 -86400 到 86400 之间的秒数"
	assertAPIErrors(t, srv, []apiError{
		{http.MethodPatch, "/api/bindings/1", `{"offset": 86400.5}`, http.StatusBadRequest, invalidOffset},
		{http.MethodPatch, "/api/bindings/1", `{"offset": -86401}`, http.StatusBadRequest, invalidOffset},
		{http.MethodPatch, "/api/bindings/1", `{}`, http.StatusBadRequest, invalidOffset},
		{http.MethodPatch, "/api/bindings/1", `{"offset": null}`, http.StatusBadRequest, invalidOffset},
		// JSON 写不出 NaN 和无穷大；超出 float64 范围的数解析失败
		{http.MethodPatch, "/api/bindings/1", `{"offset": 1e999}`, http.StatusBadRequest, "请求参数错误"},
		{http.MethodPatch, "/api/bindings/1", `{"offset": "1"}`, http.StatusBadRequest, "请求参数错误"},
		{http.MethodPatch, "/api/bindings/0", `{"offset": 1}`, http.StatusBadRequest, "绑定 ID 不合法"},
		{http.MethodPatch, "/api/bindings/99", `{"offset": 1}`, http.StatusNotFound, "绑定不存在"},
	})
}

func TestDeleteBinding(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	if code, _, data := call(t, srv, http.MethodDelete, "/api/bindings/1", "", http.StatusOK); code != 0 || string(data) != "null" {
		t.Errorf("code=%d data=%s, want 0 null", code, data)
	}
	// 它的弹幕一起删除，见 service 的 TestDeleteBinding
	assertAPIErrors(t, srv, []apiError{
		{http.MethodDelete, "/api/bindings/1", "", http.StatusNotFound, "绑定不存在"},
		{http.MethodDelete, "/api/bindings/0", "", http.StatusBadRequest, "绑定 ID 不合法"},
		{http.MethodDelete, "/api/bindings/abc", "", http.StatusBadRequest, "请求参数错误"},
	})
}
