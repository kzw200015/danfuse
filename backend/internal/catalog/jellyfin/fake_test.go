package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/config"
)

var update = flag.Bool("update", false,
	"只作用于 TestListSamples：从 e2e 环境的 Jellyfin 重新抓取样本，覆盖 testdata/<版本>/（需要先按 e2e/README.md 搭好环境）；"+
		"其他用例始终只回放。TestListSamples 的请求覆盖了其他用例用到的全部样本")

// versions 样本覆盖的 Jellyfin 版本：testdata 子目录名，以及 e2e/.env 里地址和 API key 的变量前缀。
var versions = []struct{ name, envPrefix string }{
	{"10.11", "JELLYFIN_10_11"},
	{"12.1", "JELLYFIN_12_1"},
}

const testAPIKey = "test-api-key"

// fakeJellyfin 假的 Jellyfin：检查收到的每个请求都符合适配的约定，按请求对应的样本名返回 JSON 或图片，并记下请求顺序。
type fakeJellyfin struct {
	t       *testing.T
	url     string
	apiKey  string
	respond respondFunc

	mu       sync.Mutex
	requests []string // 收到的请求，记的是样本名
	failOn   string   // 请求的样本名等于它时返回 500，模拟请求失败
}

// respondFunc 按样本名给出响应体和 Content-Type，找不到时 body 返回 nil。
// contentType 为空时按样本给出：JSON 样本为 JSON，图片样本由 http.DetectContentType 按内容识别（回放的样本没有记下响应头）。
type respondFunc func(name string, r *http.Request) (body []byte, contentType string)

func startFake(t *testing.T, apiKey string, respond respondFunc) *fakeJellyfin {
	t.Helper()
	f := &fakeJellyfin{t: t, apiKey: apiKey, respond: respond}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func (f *fakeJellyfin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	checkRequest(f.t, r, f.apiKey)
	name := sampleName(r)
	f.mu.Lock()
	f.requests = append(f.requests, name)
	fail := name == f.failOn
	f.mu.Unlock()

	if fail {
		http.Error(w, `"boom"`, http.StatusInternalServerError)
		return
	}
	body, contentType := f.respond(name, r)
	if body == nil {
		f.t.Errorf("没有请求 %s 对应的样本 %s", r.URL, name)
		http.NotFound(w, r)
		return
	}
	switch {
	case contentType != "":
	case strings.HasSuffix(name, ".json"):
		contentType = "application/json; charset=utf-8"
	default:
		contentType = http.DetectContentType(body)
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(body)
}

// newFake 回放 samples（样本名 → 响应体）的假 Jellyfin，用于构造 e2e 环境里没有的情形。
// Content-Type 按样本给出，见 respondFunc。
func newFake(t *testing.T, samples map[string]string) *fakeJellyfin {
	t.Helper()
	return startFake(t, testAPIKey, func(name string, _ *http.Request) ([]byte, string) {
		if body, ok := samples[name]; ok {
			return []byte(body), ""
		}
		return nil, ""
	})
}

// replayFake 回放 testdata/<version>/ 里抓取的样本，Content-Type 按样本给出，见 respondFunc。
func replayFake(t *testing.T, version string) *fakeJellyfin {
	t.Helper()
	dir := filepath.Join("testdata", version)
	return startFake(t, testAPIKey, func(name string, _ *http.Request) ([]byte, string) {
		body, err := os.ReadFile(samplePath(dir, name))
		if err != nil {
			return nil, ""
		}
		return body, ""
	})
}

// imageExts 录制时图片样本按 Content-Type 取的扩展名，方便直接打开查看。
var imageExts = map[string]string{"image/jpeg": ".jpg", "image/png": ".png"}

// samplePath 样本名对应的文件：JSON 样本就是样本名本身；图片样本的文件名是样本名加上扩展名（见 imageExts）。
func samplePath(dir, name string) string {
	if strings.HasSuffix(name, ".json") {
		return filepath.Join(dir, name)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, name+".*"))
	if len(matches) != 1 {
		return filepath.Join(dir, name)
	}
	return matches[0]
}

// recordFake 把请求转发给 e2e 环境里这个版本的 Jellyfin，并把响应写进 testdata/<version>/；Content-Type 沿用 Jellyfin 的响应头。
// 先确认实例可用，再清空这个版本的样本目录，不再发出的请求不会留下过时的文件。
func recordFake(t *testing.T, version string) *fakeJellyfin {
	t.Helper()
	baseURL, apiKey := e2eInstance(t, version)
	if _, _, err := fetch(t.Context(), baseURL+"/Library/VirtualFolders", http.Header{"Authorization": {authorization(apiKey)}}); err != nil {
		t.Fatalf("e2e 环境的 Jellyfin %s 不可用：%v", version, err)
	}
	dir := filepath.Join("testdata", version)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	return startFake(t, apiKey, func(name string, r *http.Request) ([]byte, string) {
		body, contentType, err := fetch(r.Context(), baseURL+r.URL.RequestURI(), r.Header)
		if err != nil {
			t.Errorf("转发 %s 到 Jellyfin %s：%v", r.URL, version, err)
			return nil, ""
		}
		sample, file := body, name
		if strings.HasSuffix(name, ".json") {
			if sample, err = readable(body); err != nil {
				t.Errorf("Jellyfin %s 的响应不是 JSON：%v", version, err)
				return nil, ""
			}
		} else {
			ext, ok := imageExts[contentType]
			if !ok {
				t.Errorf("Jellyfin %s 返回的图片格式 %q 没有对应的扩展名", version, contentType)
				return nil, ""
			}
			file += ext
		}
		if err := os.WriteFile(filepath.Join(dir, file), sample, 0o644); err != nil {
			t.Error(err)
		}
		return sample, contentType
	})
}

// readable 把响应体重新编码成便于阅读的样本：缩进，中文不转义成 \uXXXX。
// 对象的键会按字母排序，数组顺序和数值原样保留。
func readable(body []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// fetch 向 e2e 环境的 Jellyfin 发 GET，只带上 header 里的鉴权和 Accept 请求头，返回响应体和 Content-Type；
// Accept-Encoding 交给 Transport，响应才会自动解压。
func fetch(ctx context.Context, target string, header http.Header) (body []byte, contentType string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	for _, h := range []string{"Authorization", "Accept"} {
		if v := header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("status %s", resp.Status)
	}
	body, err = io.ReadAll(resp.Body)
	return body, resp.Header.Get("Content-Type"), err
}

// e2eInstance 从 e2e/.env 读出某个版本的 Jellyfin 地址和 API key。
func e2eInstance(t *testing.T, version string) (baseURL, apiKey string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "e2e", ".env"))
	if err != nil {
		t.Fatalf("-update 需要 e2e 环境：%v", err)
	}
	env := map[string]string{}
	for line := range strings.Lines(string(data)) {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			env[k] = v
		}
	}
	for _, v := range versions {
		if v.name == version {
			baseURL, apiKey = env[v.envPrefix+"_URL"], env[v.envPrefix+"_API_KEY"]
		}
	}
	if baseURL == "" || apiKey == "" {
		t.Fatalf("e2e/.env 里没有 Jellyfin %s 的地址或 API key", version)
	}
	return baseURL, apiKey
}

// sampleName 请求对应的样本名：每个请求由路径和 ParentId（海报为条目 Id）唯一确定。
func sampleName(r *http.Request) string {
	switch r.URL.Path {
	case "/Library/VirtualFolders":
		return "virtual-folders.json"
	case "/Items":
		return "items-" + r.URL.Query().Get("ParentId") + ".json"
	}
	if id, ok := posterItemID(r.URL.Path); ok {
		return "image-" + id
	}
	return r.URL.Path
}

// posterItemID 从海报的请求路径 /Items/{Id}/Images/Primary 里取出条目 Id。
func posterItemID(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/Items/")
	if !ok {
		return "", false
	}
	id, ok := strings.CutSuffix(rest, "/Images/Primary")
	return id, ok && id != "" && !strings.Contains(id, "/")
}

func authorization(apiKey string) string { return `MediaBrowser Token="` + apiKey + `"` }

// checkRequest 检查适配发出的请求：鉴权只用请求头；不带 userId、不分页；显式递归；每一步的参数与 spec 一致；
// 不声明接受 WebP（否则 Jellyfin 会把海报都转成 WebP）。
func checkRequest(t *testing.T, r *http.Request, apiKey string) {
	t.Helper()
	if r.Method != http.MethodGet {
		t.Errorf("%s %s：只应发 GET", r.Method, r.URL)
	}
	if got, want := r.Header.Get("Authorization"), authorization(apiKey); got != want {
		t.Errorf("%s：Authorization = %q, want %q", r.URL.Path, got, want)
	}
	if accept := r.Header.Get("Accept"); strings.Contains(accept, "image/webp") {
		t.Errorf("%s：不应声明接受 WebP（Accept: %s）", r.URL.Path, accept)
	}

	query := r.URL.Query()
	switch r.URL.Path {
	case "/Library/VirtualFolders":
		if len(query) != 0 {
			t.Errorf("/Library/VirtualFolders 不应带参数：%s", r.URL.RawQuery)
		}
	case "/Items":
		want := url.Values{
			"ParentId":  {query.Get("ParentId")},
			"Recursive": {"true"},
			"IsMissing": {"false"},
		}
		switch query.Get("IncludeItemTypes") {
		case "Series,Movie": // 列出媒体库里的剧和电影
			want.Set("IncludeItemTypes", "Series,Movie")
			want.Set("Fields", "OriginalTitle")
		case "Season,Episode": // 按剧取季和集
			want.Set("IncludeItemTypes", "Season,Episode")
		}
		if query.Get("ParentId") == "" || query.Encode() != want.Encode() {
			t.Errorf("/Items 的参数 = %s, want %s", query.Encode(), want.Encode())
		}
	default:
		if _, ok := posterItemID(r.URL.Path); !ok {
			t.Errorf("不应请求 %s", r.URL.Path)
		} else if want := (url.Values{"maxWidth": {"400"}}); query.Encode() != want.Encode() {
			t.Errorf("%s 的参数 = %s, want %s", r.URL.Path, query.Encode(), want.Encode())
		}
	}
}

// source 连到这个假 Jellyfin 的目录源。
func (f *fakeJellyfin) source(libraries ...string) *Source {
	return New(config.Jellyfin{URL: f.url, APIKey: f.apiKey, Libraries: libraries})
}

func (f *fakeJellyfin) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeJellyfin) failRequest(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failOn = name
}
