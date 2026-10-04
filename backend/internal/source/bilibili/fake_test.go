package bilibili

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/protobuf/encoding/protowire"
)

// response 假 B 站的一个响应。
type response struct {
	status      int
	contentType string
	body        []byte
}

// respondFunc 按样本名给出响应，找不到时返回 false。
type respondFunc func(name string, r *http.Request) (response, bool)

// fakeBilibili 假的 B 站 API：检查收到的每个请求都带着浏览器 UA 和 Referer、参数与约定一致，
// 按请求对应的样本名给出响应，并记下请求顺序与同时处理的请求数。
type fakeBilibili struct {
	t       *testing.T
	url     string
	respond respondFunc
	hold    time.Duration // 每个请求处理前停这么久，用来观察并发

	mu          sync.Mutex
	requests    []string // 收到的请求，记的是样本名
	inflight    int
	maxInflight int
}

func startFake(t *testing.T, respond respondFunc) *fakeBilibili {
	t.Helper()
	f := &fakeBilibili{t: t, respond: respond}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func (f *fakeBilibili) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	checkRequest(f.t, r)
	name := sampleName(r)
	f.mu.Lock()
	f.requests = append(f.requests, name)
	f.inflight++
	f.maxInflight = max(f.maxInflight, f.inflight)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.inflight--
		f.mu.Unlock()
	}()

	time.Sleep(f.hold)
	resp, ok := f.respond(name, r)
	if !ok {
		f.t.Errorf("没有请求 %s 对应的样本 %s", r.URL, name)
		http.NotFound(w, r)
		return
	}
	if resp.contentType != "" {
		w.Header().Set("Content-Type", resp.contentType)
	}
	w.WriteHeader(resp.status)
	_, _ = w.Write(resp.body)
}

// newFake 按 samples（样本名 → 依次给出的响应）构造响应：第 i 次请求某个样本给出第 i 个响应，用完后一直给最后一个。
func newFake(t *testing.T, samples map[string][]response) *fakeBilibili {
	t.Helper()
	var mu sync.Mutex
	served := map[string]int{}
	return startFake(t, func(name string, _ *http.Request) (response, bool) {
		seq, ok := samples[name]
		if !ok {
			return response{}, false
		}
		mu.Lock()
		defer mu.Unlock()
		i := min(served[name], len(seq)-1)
		served[name]++
		return seq[i], true
	})
}

// replayFake 回放 testdata/ 里录制的样本：<样本名>.json、<样本名>.bin 按 200 返回，<样本名>.304 返回 304。
func replayFake(t *testing.T) *fakeBilibili {
	t.Helper()
	return startFake(t, func(name string, _ *http.Request) (response, bool) {
		for ext, ct := range sampleTypes {
			body, err := os.ReadFile(filepath.Join("testdata", name+ext))
			if err != nil {
				continue
			}
			if ext == ".304" {
				return response{status: http.StatusNotModified}, true
			}
			return response{status: http.StatusOK, contentType: ct, body: body}, true
		}
		return response{}, false
	})
}

// sampleTypes 样本文件的扩展名与回放时的 Content-Type。
var sampleTypes = map[string]string{
	".json": "application/json; charset=utf-8",
	".bin":  "application/octet-stream",
	".304":  "",
}

// adapter 连到这个假 B 站的适配器。令牌桶不限速、重试不等待，测试不因时间参数变慢。
func (f *fakeBilibili) adapter() *Adapter {
	a := New()
	a.client.baseURL = f.url
	a.client.limiter = rate.NewLimiter(rate.Inf, 0)
	a.client.retryDelay = 0
	return a
}

func (f *fakeBilibili) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// sampleName 请求对应的样本名：view-<aid>、seg-<cid>-<段号>。
func sampleName(r *http.Request) string {
	q := r.URL.Query()
	switch r.URL.Path {
	case viewPath:
		return "view-" + q.Get("aid")
	case segPath:
		return "seg-" + q.Get("oid") + "-" + q.Get("segment_index")
	default:
		return r.URL.Path
	}
}

const (
	viewPath = "/x/web-interface/view"
	segPath  = "/x/v2/dm/web/seg.so"
)

var number = regexp.MustCompile(`^[1-9][0-9]*$`)

// checkRequest 检查适配器发出的请求：只发 GET，带浏览器 UA 和 Referer，每个接口的参数与约定一致。
func checkRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodGet {
		t.Errorf("%s %s：只应发 GET", r.Method, r.URL)
	}
	if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "Mozilla/5.0 ") {
		t.Errorf("%s：User-Agent = %q, want 浏览器 UA", r.URL.Path, ua)
	}
	if got := r.Header.Get("Referer"); got != "https://www.bilibili.com" {
		t.Errorf("%s：Referer = %q, want https://www.bilibili.com", r.URL.Path, got)
	}

	q := r.URL.Query()
	var want url.Values
	switch r.URL.Path {
	case viewPath:
		want = url.Values{"aid": {q.Get("aid")}}
		if !number.MatchString(q.Get("aid")) {
			t.Errorf("view 的 aid = %q", q.Get("aid"))
		}
	case segPath:
		want = url.Values{"type": {"1"}, "oid": {q.Get("oid")}, "segment_index": {q.Get("segment_index")}}
		if !number.MatchString(q.Get("oid")) || !number.MatchString(q.Get("segment_index")) {
			t.Errorf("seg.so 的 oid = %q, segment_index = %q", q.Get("oid"), q.Get("segment_index"))
		}
	default:
		t.Errorf("不应请求 %s", r.URL.Path)
		return
	}
	if q.Encode() != want.Encode() {
		t.Errorf("%s 的参数 = %s, want %s", r.URL.Path, q.Encode(), want.Encode())
	}
}

// --- 构造响应 ---

func jsonResponse(body string) response {
	return response{status: http.StatusOK, contentType: "application/json; charset=utf-8", body: []byte(body)}
}

// codeResponse B 站 JSON 接口返回业务错误码。
func codeResponse(code int) response {
	return jsonResponse(fmt.Sprintf(`{"code":%d,"message":"错误 %d","ttl":1}`, code, code))
}

// viewResponse view 接口的成功响应。
func viewResponse(t *testing.T, title string, pages ...viewPage) response {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"code": 0, "message": "0", "ttl": 1,
		"data": map[string]any{"aid": 1, "title": title, "duration": 99999, "pages": pages},
	})
	if err != nil {
		t.Fatal(err)
	}
	return jsonResponse(string(body))
}

func statusResponse(status int) response {
	return response{status: status, contentType: "text/html", body: []byte("<html>error</html>")}
}

// segResponse seg.so 的成功响应，elems 是编码好的 DanmakuElem。
func segResponse(elems ...[]byte) response {
	var b []byte
	for _, e := range elems {
		b = protowire.AppendTag(b, replyElems, protowire.BytesType)
		b = protowire.AppendBytes(b, e)
	}
	return response{status: http.StatusOK, contentType: "application/octet-stream", body: b}
}

// elem 构造一条 DanmakuElem 用到的字段；为 0 或空的字段不编码，与 proto3 一致。
type elem struct {
	id       int64
	progress int32
	mode     int32
	color    uint32
	content  string
	extra    []byte // 追加在末尾的其他字段
}

func (e elem) encode() []byte {
	var b []byte
	varint := func(num protowire.Number, v uint64) {
		if v != 0 {
			b = protowire.AppendTag(b, num, protowire.VarintType)
			b = protowire.AppendVarint(b, v)
		}
	}
	varint(elemID, uint64(e.id))
	varint(elemProgress, uint64(int64(e.progress))) // int32 的负数按 10 字节 varint 编码
	varint(elemMode, uint64(int64(e.mode)))
	varint(elemColor, uint64(e.color))
	if e.content != "" {
		b = protowire.AppendTag(b, elemContent, protowire.BytesType)
		b = protowire.AppendString(b, e.content)
	}
	return append(b, e.extra...)
}
