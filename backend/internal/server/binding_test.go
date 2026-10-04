package server

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// fakeAdapter 假的源适配器：链接 "fake/<名字>" 的 ref 为 {"name":"<名字>"}。
// Fetch 对名字 gone 返回 NotFound、对 down 返回 Upstream，其余返回两条弹幕。
type fakeAdapter struct{}

type fakeRef struct {
	Name string `json:"name"`
}

func (fakeAdapter) ID() string                 { return "fake" }
func (fakeAdapter) Platform() danmaku.Platform { return danmaku.PlatformNone }

func (fakeAdapter) Describe(ref source.Ref) (source.Display, error) {
	var r fakeRef
	err := json.Unmarshal(ref, &r)
	return source.Display{URL: "https://fake.test/" + r.Name, Label: "假来源 " + r.Name}, err
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

// postJSON 发一个带 JSON 请求体的 POST，检查状态码，返回解出的统一响应。
func postJSON(t *testing.T, srv *Server, target, body string, wantStatus int) (code int, message string, data json.RawMessage) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	srv.echo.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("POST %s %s: status = %d, want %d, body %s", target, body, rec.Code, wantStatus, rec.Body)
	}
	var resp struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("POST %s: 不是统一响应：%s", target, rec.Body)
	}
	return resp.Code, resp.Message, resp.Data
}

func TestCreateBinding(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	seedCatalog(t, pool)
	srv := catalogServer(pool)

	// 201 和绑定：不含 ref 和 contentVersion
	_, _, data := postJSON(t, srv, "/api/episodes/2/bindings", `{"url": " fake/x "}`, http.StatusCreated)
	binding := decodeObject(t, data)
	wantFields := []string{
		"adapter", "danmakuCount", "duration", "id", "lastFetchedAt", "offset",
		"sourceLabel", "sourceUrl", "status", "title",
	}
	if got := slices.Sorted(maps.Keys(binding)); !slices.Equal(got, wantFields) {
		t.Errorf("绑定的字段 = %q\nwant %q", got, wantFields)
	}
	if string(binding["lastFetchedAt"]) == "null" {
		t.Error("lastFetchedAt 为 null，want 这次拉取的时间")
	}
	delete(binding, "lastFetchedAt")
	assertJSON(t, json.RawMessage(jsonString(binding)), `{
		"id": 5, "adapter": "fake", "sourceUrl": "https://fake.test/x", "sourceLabel": "假来源 x",
		"title": "弹幕源 x", "duration": 1418, "offset": 0, "status": "active", "danmakuCount": 2
	}`)

	// 剧列表随之更新
	_, _, data = call(t, srv, http.MethodGet, "/api/series", http.StatusOK)
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
		{"/api/episodes/2/bindings", `{"url": "fake/x"}`, http.StatusConflict, "这一集已经绑定过这个来源"},
		{"/api/episodes/2/bindings", `{"url": "https://example.com/v/1"}`, http.StatusBadRequest, "无法识别的链接"},
		{"/api/episodes/2/bindings", `{"url": "  "}`, http.StatusBadRequest, "请粘贴弹幕源的链接"},
		{"/api/episodes/2/bindings", `{"url": `, http.StatusBadRequest, "请求参数错误"},
		{"/api/episodes/0/bindings", `{"url": "fake/x"}`, http.StatusBadRequest, "集 ID 不合法"},
		{"/api/episodes/99/bindings", `{"url": "fake/x"}`, http.StatusNotFound, "集不存在"},
		{"/api/episodes/2/bindings", `{"url": "fake/gone"}`, http.StatusUnprocessableEntity, "视频不存在、已删除或不可见"},
		{"/api/episodes/2/bindings", `{"url": "fake/down"}`, http.StatusBadGateway, "B 站接口异常"},
	} {
		if code, message, _ := postJSON(t, srv, tt.target, tt.body, tt.wantStatus); code != 1 || message != tt.wantMessage {
			t.Errorf("POST %s %s: code=%d message=%q, want %q", tt.target, tt.body, code, message, tt.wantMessage)
		}
	}
}
