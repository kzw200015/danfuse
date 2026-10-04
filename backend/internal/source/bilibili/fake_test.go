package bilibili

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/kzw200015/danfuse/backend/internal/config"
)

// response 假 B 站的一个响应。
type response struct {
	status          int
	contentType     string
	contentEncoding string
	location        string // 短链跳转的目标
	body            []byte
}

// respondFunc 按样本名给出响应，找不到时返回 false。
type respondFunc func(name string, r *http.Request) (response, bool)

// fakeBilibili 假的 B 站：api.bilibili.com、comment.bilibili.com 和短链域名都由它应答。
// 检查收到的每个请求都带着浏览器 UA 和 Referer、Cookie 与参数符合约定，按请求对应的样本名给出响应，
// 并记下请求顺序与同时处理的请求数。
type fakeBilibili struct {
	t        *testing.T
	url      string
	respond  respondFunc
	hold     time.Duration // 每个请求处理前停这么久，用来观察并发
	sessdata string        // 适配器配置的 SESSDATA；发往 bilibili.com 的请求应带上它，其他请求不带 Cookie

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
	f.checkRequest(r)
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
		f.t.Errorf("没有请求 %s%s 对应的样本 %s", r.Host, r.URL, name)
		http.NotFound(w, r)
		return
	}
	for k, v := range map[string]string{"Content-Type": resp.contentType, "Content-Encoding": resp.contentEncoding, "Location": resp.location} {
		if v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.status)
	_, _ = w.Write(resp.body)
}

// transport 把适配器发往各个域名的请求都转给这个假 B 站：只换掉连接的地址，Host 仍是原来的域名，假 B 站据此区分接口。
func (f *fakeBilibili) transport() http.RoundTripper {
	target, err := url.Parse(f.url)
	if err != nil {
		f.t.Fatal(err)
	}
	return roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r)
	})
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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

// replayFake 回放 testdata/ 里录制的样本，扩展名决定响应的形式，见 sampleTypes。
func replayFake(t *testing.T) *fakeBilibili {
	t.Helper()
	return startFake(t, func(name string, _ *http.Request) (response, bool) {
		for ext, toResponse := range sampleTypes {
			body, err := os.ReadFile(filepath.Join("testdata", name+ext))
			if err == nil {
				return toResponse(t, body), true
			}
		}
		return response{}, false
	})
}

// sampleTypes 样本文件的扩展名，以及回放时由文件内容得到的响应。
var sampleTypes = map[string]func(t *testing.T, body []byte) response{
	".json": func(_ *testing.T, body []byte) response { return jsonResponse(string(body)) },
	".bin": func(_ *testing.T, body []byte) response {
		return response{status: http.StatusOK, contentType: "application/octet-stream", body: body}
	},
	".304": func(*testing.T, []byte) response { return response{status: http.StatusNotModified} },
	".xml": xmlResponse,                                                                        // 样本存解压后的 XML，回放时压缩
	".302": func(_ *testing.T, body []byte) response { return redirectResponse(string(body)) }, // 样本存跳转的目标
}

// adapter 连到这个假 B 站的适配器。令牌桶不限速、重试不等待，测试不因时间参数变慢。
func (f *fakeBilibili) adapter() *Adapter {
	a := New(config.Bilibili{Sessdata: f.sessdata})
	a.client.http.Transport = f.transport()
	a.client.limiter = rate.NewLimiter(rate.Inf, 0)
	a.client.retryDelay = 0
	return a
}

func (f *fakeBilibili) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// sampleName 请求对应的样本名：view-<aid>、pgc-<ep_id>、seg-<cid>-<段号>、xml-<cid>，短链为 short-<域名>-<路径>。
func sampleName(r *http.Request) string {
	q := r.URL.Query()
	switch {
	case slices.Contains(shortLinkHosts, r.Host):
		return "short-" + r.Host + "-" + strings.ReplaceAll(strings.Trim(r.URL.Path, "/"), "/", "_")
	case r.Host == "comment.bilibili.com":
		return "xml-" + strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".xml")
	case r.URL.Path == viewPath:
		return "view-" + q.Get("aid")
	case r.URL.Path == pgcPath:
		return "pgc-" + q.Get("ep_id")
	case r.URL.Path == segPath:
		return "seg-" + q.Get("oid") + "-" + q.Get("segment_index")
	default:
		return r.Host + r.URL.Path
	}
}

const (
	viewPath = "/x/web-interface/view"
	pgcPath  = "/pgc/view/web/season"
	segPath  = "/x/v2/dm/web/seg.so"
)

var (
	number    = regexp.MustCompile(`^[1-9][0-9]*$`)
	xmlPath   = regexp.MustCompile(`^/[1-9][0-9]*\.xml$`)
	shortPath = regexp.MustCompile(`^/[0-9A-Za-z]+$`)
)

// checkRequest 检查适配器发出的请求：只发 GET，带浏览器 UA 和 Referer；
// 发往 bilibili.com 的请求在配置了 SESSDATA 时带上它，短链不带任何 Cookie；每个接口的参数与约定一致。
func (f *fakeBilibili) checkRequest(r *http.Request) {
	t := f.t
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
	wantCookie := ""
	if f.sessdata != "" && strings.HasSuffix(r.Host, ".bilibili.com") {
		wantCookie = "SESSDATA=" + f.sessdata
	}
	if got := r.Header.Get("Cookie"); got != wantCookie {
		t.Errorf("%s%s：Cookie = %q, want %q", r.Host, r.URL.Path, got, wantCookie)
	}

	q := r.URL.Query()
	var want url.Values
	switch {
	case slices.Contains(shortLinkHosts, r.Host):
		if !shortPath.MatchString(r.URL.Path) {
			t.Errorf("短链的路径 = %q", r.URL.Path)
		}
	case r.Host == "comment.bilibili.com":
		if !xmlPath.MatchString(r.URL.Path) {
			t.Errorf("XML 的路径 = %q", r.URL.Path)
		}
	case r.Host != "api.bilibili.com":
		t.Errorf("不应请求 %s", r.Host)
	case r.URL.Path == viewPath:
		want = url.Values{"aid": {q.Get("aid")}}
		if !number.MatchString(q.Get("aid")) {
			t.Errorf("view 的 aid = %q", q.Get("aid"))
		}
	case r.URL.Path == pgcPath:
		want = url.Values{"ep_id": {q.Get("ep_id")}}
		if !number.MatchString(q.Get("ep_id")) {
			t.Errorf("pgc 的 ep_id = %q", q.Get("ep_id"))
		}
	case r.URL.Path == segPath:
		want = url.Values{"type": {"1"}, "oid": {q.Get("oid")}, "segment_index": {q.Get("segment_index")}}
		if !number.MatchString(q.Get("oid")) || !number.MatchString(q.Get("segment_index")) {
			t.Errorf("seg.so 的 oid = %q, segment_index = %q", q.Get("oid"), q.Get("segment_index"))
		}
	default:
		t.Errorf("不应请求 %s%s", r.Host, r.URL.Path)
		return
	}
	if q.Encode() != want.Encode() {
		t.Errorf("%s%s 的参数 = %s, want %s", r.Host, r.URL.Path, q.Encode(), want.Encode())
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
	return viewRedirectResponse(t, title, "", pages...)
}

// viewRedirectResponse 番剧的稿件：view 的响应带 redirect_url。
func viewRedirectResponse(t *testing.T, title, redirectURL string, pages ...viewPage) response {
	t.Helper()
	data := map[string]any{"aid": 1, "title": title, "duration": 99999, "pages": pages}
	if redirectURL != "" {
		data["redirect_url"] = redirectURL
	}
	return marshalResponse(t, map[string]any{"code": 0, "message": "0", "ttl": 1, "data": data})
}

// pgcResponse pgc 接口的成功响应，根对象用 result。
func pgcResponse(t *testing.T, s seasonData) response {
	t.Helper()
	return marshalResponse(t, map[string]any{"code": 0, "message": "success", "result": s})
}

func marshalResponse(t *testing.T, v any) response {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return jsonResponse(string(body))
}

func statusResponse(status int) response {
	return response{status: status, contentType: "text/html", body: []byte("<html>error</html>")}
}

func redirectResponse(location string) response {
	return response{status: http.StatusFound, contentType: "text/html; charset=utf-8", location: location}
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

// xmlResponse XML 弹幕的成功响应：与 B 站一样用 raw deflate 压缩。
func xmlResponse(t *testing.T, doc []byte) response {
	t.Helper()
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(doc); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return response{status: http.StatusOK, contentType: "text/xml", contentEncoding: "deflate", body: buf.Bytes()}
}

// xmlDoc 拼出一份 XML 弹幕，ds 是编码好的 <d> 元素。
func xmlDoc(ds ...string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?><i><chatserver>chat.bilibili.com</chatserver><chatid>101</chatid>` +
		`<mission>0</mission><maxlimit>1000</maxlimit><state>0</state><real_name>0</real_name><source>k-v</source>` +
		strings.Join(ds, "") + `</i>`)
}

// emptyXML 没有弹幕的 XML 弹幕。
func emptyXML(t *testing.T) response {
	t.Helper()
	return xmlResponse(t, xmlDoc())
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
