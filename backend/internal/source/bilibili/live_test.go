package bilibili

// live 模式：请求真实的 B 站，确认适配器在一份固定的公开视频列表上仍然正确。默认跳过，CI 不请求 B 站。
//
//	go test ./internal/source/bilibili -run TestLive -args -live           # 只验证
//	go test ./internal/source/bilibili -run TestLive -args -live -update   # 验证，并重新录制 testdata/
//
// 在里程碑验收时和改动 B 站适配器之后各跑一遍。全部用例约 10 次请求，靠适配器自己的令牌桶限速。
// 列表里的视频状态变了（被删、开关弹幕、改标题）就换一个，再加 -update 重新录制。
//
// -update 只在 -live 时生效：先清空 testdata/，再把 B 站的响应脱敏后写进去，原始响应不落盘；
// 限流、接口异常这类出错的响应不录制，原样交给适配器重试。
//   - view：只保留适配器会读的字段；标记了 redactTitle 的视频，标题也换成占位文本；
//   - seg.so：解码后每段只留前 sampleElems 条；正文依次换成"弹幕1""弹幕2"……，每段的前几条换成 specialTexts；
//     发送者哈希换成固定值；ID、时间、模式、颜色等其余字段和其他顶层字段原样保留，再重新编码。
//
// TestSamples 平时回放这些样本，断言与 live 模式相同，另外检查特殊正文原样保留。

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	live   = flag.Bool("live", false, "TestLive 请求真实的 B 站；不加时跳过")
	update = flag.Bool("update", false, "与 -live 一起用：把 B 站的响应脱敏后写进 testdata/，覆盖原有样本")
)

// liveCases 固定的公开视频列表。挑的都是多年不变的视频：官方账号的 MV、2012 年的多 P 老投稿、
// 一直不可见的 av1、从未存在的 aid、长期关闭弹幕的官方视频。
var liveCases = []struct {
	name        string
	link        string
	ref         string      // 期望的规范化 ref
	kind        source.Kind // 不为 0 时期望 Fetch 返回这一类错误
	title       string
	redactTitle bool // 标题与本项目无关：录制时样本里的标题换成 redactedTitle，断言只检查标题不为空
	duration    int
	closed      bool // 弹幕已关闭：拉取成功、0 条
}{
	{
		name: "单 P 投稿的 BV 链接", link: "https://www.bilibili.com/video/BV1GJ411x7h7/",
		ref:   `{"kind":"video","aid":80433022,"page":1}`,
		title: "【官方 MV】Never Gonna Give You Up - Rick Astley", duration: 213,
	},
	{
		name: "多 P 投稿带 ?p=3", link: "https://www.bilibili.com/video/BV17x411w7KC?p=3",
		ref:   `{"kind":"video","aid":170001,"page":3}`,
		title: "【MV】保加利亚妖王AZIS视频合辑 / No Kazvam Ti Stiga", duration: 308,
	},
	{
		name: "av 链接", link: "https://www.bilibili.com/video/av170001",
		ref:   `{"kind":"video","aid":170001,"page":1}`,
		title: "【MV】保加利亚妖王AZIS视频合辑 / Хоп", duration: 199,
	},
	{
		name: "已删除的视频：稿件不可见", link: "https://www.bilibili.com/video/BV1xx411c7mQ", // av1，62002
		ref:  `{"kind":"video","aid":1,"page":1}`,
		kind: source.NotFound,
	},
	{
		name: "已删除的视频：不存在", link: "av999999999999", // -404
		ref:  `{"kind":"video","aid":999999999999,"page":1}`,
		kind: source.NotFound,
	},
	{
		name: "弹幕已关闭的视频", link: "https://www.bilibili.com/video/BV1Hs6HYqEYs",
		ref:         `{"kind":"video","aid":113747125342871,"page":1}`,
		redactTitle: true, duration: 607, closed: true,
	},
}

func TestLive(t *testing.T) {
	if !*live {
		t.Skip("live 模式默认关闭，用 -args -live 打开")
	}
	a := New()
	if *update {
		a.client.baseURL = recordFake(t).url
	}
	checkCases(t, a)
}

func TestSamples(t *testing.T) {
	results := checkCases(t, replayFake(t).adapter())

	// 样本每段只留前 sampleElems 条，都是普通的文字弹幕，一条不少
	for _, name := range []string{"单 P 投稿的 BV 链接", "多 P 投稿带 ?p=3", "av 链接"} {
		if n := len(results[name].Danmaku); n != sampleElems {
			t.Errorf("%s：%d 条弹幕，want %d", name, n, sampleElems)
		}
	}
	// 已知弹幕的全部字段，包括缺省为 0 的时间和颜色
	for _, tt := range []struct {
		name  string
		index int
		want  danmaku.Danmaku
	}{
		{"单 P 投稿的 BV 链接", 170, danmaku.Danmaku{SourceID: 44722872605212679, TimeMs: 0, Mode: danmaku.ModeBottom, Color: 0xE70012, Text: "弹幕171"}},
		{"多 P 投稿带 ?p=3", 95, danmaku.Danmaku{SourceID: 869452471, TimeMs: 120642, Mode: danmaku.ModeBottom, Color: 0, Text: "弹幕96"}},
	} {
		if got := results[tt.name].Danmaku; len(got) <= tt.index || got[tt.index] != tt.want {
			t.Errorf("%s：第 %d 条弹幕不符，want %+v", tt.name, tt.index+1, tt.want)
		}
	}
	// 特殊正文原样保留
	for name, got := range results {
		if len(got.Danmaku) == 0 {
			continue
		}
		for _, text := range specialTexts {
			if !slices.ContainsFunc(got.Danmaku, func(d danmaku.Danmaku) bool { return d.Text == text }) {
				t.Errorf("%s：没有找到特殊正文 %q", name, text)
			}
		}
	}
}

// checkCases 逐个解析、拉取 liveCases 并断言，返回拉取成功的结果。
func checkCases(t *testing.T, a *Adapter) map[string]source.Fetched {
	t.Helper()
	results := map[string]source.Fetched{}
	registry := source.NewRegistry(a)
	for _, tc := range liveCases {
		t.Run(tc.name, func(t *testing.T) {
			_, ref, err := registry.ParseLink(t.Context(), tc.link)
			if err != nil || string(ref) != tc.ref {
				t.Fatalf("ParseLink(%q) = (%s, %v), want %s", tc.link, ref, err, tc.ref)
			}

			got, err := a.Fetch(t.Context(), ref)
			if tc.kind != 0 {
				assertError(t, err, tc.kind)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%q，%d 秒，%d 条弹幕，%v", got.Title, got.Duration, len(got.Danmaku), got.LogAttrs)
			titleOK := got.Title == tc.title || tc.redactTitle && got.Title != ""
			if !titleOK || got.Duration != tc.duration {
				t.Errorf("Fetch() = (%q, %d 秒), want (%q, %d 秒)", got.Title, got.Duration, tc.title, tc.duration)
			}
			if tc.closed != (len(got.Danmaku) == 0) {
				t.Errorf("拉取到 %d 条弹幕，want 弹幕已关闭 = %v", len(got.Danmaku), tc.closed)
			}
			for _, d := range got.Danmaku {
				if d.SourceID == 0 || !slices.Contains([]danmaku.Mode{1, 4, 5, 6}, d.Mode) || d.Color > 0xFFFFFF ||
					!utf8.ValidString(d.Text) || strings.TrimSpace(d.Text) == "" || strings.Contains(d.Text, "\x00") {
					t.Errorf("不符合内部格式的弹幕：%+v", d)
				}
			}
			if !slices.ContainsFunc(got.LogAttrs, func(attr slog.Attr) bool {
				return attr.Key == "protobuf" && attr.Value.Int64() == int64(len(got.Danmaku))
			}) {
				t.Errorf("LogAttrs = %v, want 带上 protobuf 条数 %d", got.LogAttrs, len(got.Danmaku))
			}
			results[tc.name] = got
		})
	}
	return results
}

const sampleElems = 200 // 每段样本最多保留的弹幕条数

// specialTexts 样本每段前几条弹幕的正文，覆盖 emoji、需要转义的字符和换行。
var specialTexts = []string{"😀🎉 emoji", `&<>"' 转义`, "第一行\n第二行"}

// redactedTitle 脱敏后的视频标题与分 P 标题，见 liveCases 的 redactTitle。
const redactedTitle = "（标题已脱敏）"

// recordFake 把适配器的请求原样转发给 B 站，响应脱敏后写进 testdata/，再把脱敏后的响应交给适配器。
// 先清空 testdata/，不再发出的请求不会留下过时的样本。
func recordFake(t *testing.T) *fakeBilibili {
	t.Helper()
	if err := os.RemoveAll("testdata"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	redacted := map[string]bool{} // 要脱敏标题的 view 样本名
	for _, tc := range liveCases {
		if r, err := decodeRef(source.Ref(tc.ref)); err == nil && tc.redactTitle {
			redacted[fmt.Sprintf("view-%d", r.Aid)] = true
		}
	}
	client := &http.Client{Timeout: 30 * time.Second}

	return startFake(t, func(name string, r *http.Request) (response, bool) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, apiURL+r.URL.RequestURI(), nil)
		if err != nil {
			t.Error(err)
			return response{status: http.StatusBadGateway}, true
		}
		for _, h := range []string{"User-Agent", "Referer"} {
			req.Header.Set(h, r.Header.Get(h))
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("转发 %s：%v", name, err)
			return response{status: http.StatusBadGateway}, true
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Logf("转发 %s：%v", name, err)
			return response{status: http.StatusBadGateway}, true
		}
		got := response{status: resp.StatusCode, contentType: resp.Header.Get("Content-Type"), body: body}

		var ext string
		switch isSeg := strings.HasPrefix(name, "seg-"); {
		case !isSeg && recordableView(got):
			ext = ".json"
			got.body, err = sanitizeView(body, redacted[name])
		case isSeg && got.status == http.StatusNotModified:
			ext, got.body = ".304", nil
		case isSeg && got.status == http.StatusOK && !strings.Contains(got.contentType, "json"):
			ext = ".bin"
			got.body, err = sanitizeSegment(body)
		default:
			// 限流等出错的响应不录制，交给适配器照常重试
			t.Logf("录制 %s：B 站返回 %s %s %.200s，不写入样本", name, resp.Status, got.contentType, body)
			return got, true
		}
		if err != nil {
			t.Errorf("脱敏 %s：%v", name, err)
			return response{status: http.StatusBadGateway}, true
		}
		if err := os.WriteFile(filepath.Join("testdata", name+ext), got.body, 0o644); err != nil {
			t.Error(err)
		}
		return got, true
	})
}

// recordableView view 的响应能否录制：成功，或是 NotFound、AuthRequired 这类确定的结果；
// 限流（包括要求风控验证的 v_voucher）和接口异常不录制。
func recordableView(got response) bool {
	if got.status != http.StatusOK {
		return false
	}
	_, err := decodeEnvelope("", got.body)
	srcErr, ok := errors.AsType[*source.Error](err)
	return err == nil || ok && (srcErr.Kind == source.NotFound || srcErr.Kind == source.AuthRequired)
}

// sanitizeView view 的 JSON 只保留适配器会读的字段，重新编码成便于阅读的样本（缩进、中文不转义）。
// redactTitle 时把视频标题和分 P 标题换成 redactedTitle。
func sanitizeView(body []byte, redactTitle bool) ([]byte, error) {
	var v struct {
		Code    int       `json:"code"`
		Message string    `json:"message"`
		Data    *viewData `json:"data,omitempty"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	if redactTitle && v.Data != nil {
		v.Data.Title = redactedTitle
		for i := range v.Data.Pages {
			v.Data.Pages[i].Part = redactedTitle
		}
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

// sanitizeSegment 脱敏一段 DmSegMobileReply，规则见文件开头。
func sanitizeSegment(b []byte) ([]byte, error) {
	var out []byte
	kept := 0
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeField(b)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		raw := b[:n]
		b = b[n:]
		if num != replyElems || typ != protowire.BytesType {
			out = append(out, raw...)
			continue
		}
		if kept == sampleElems {
			continue
		}
		kept++
		text := fmt.Sprintf("弹幕%d", kept)
		if kept <= len(specialTexts) {
			text = specialTexts[kept-1]
		}
		_, _, tagLen := protowire.ConsumeTag(raw)
		elem, _ := protowire.ConsumeBytes(raw[tagLen:])
		sanitized, err := sanitizeElem(elem, text)
		if err != nil {
			return nil, err
		}
		out = protowire.AppendTag(out, replyElems, protowire.BytesType)
		out = protowire.AppendBytes(out, sanitized)
	}
	return out, nil
}

// elemMidHash DanmakuElem 里发送者 mid 的哈希（可以反查用户），样本里换成固定值。
const elemMidHash protowire.Number = 6

func sanitizeElem(b []byte, text string) ([]byte, error) {
	var out []byte
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeField(b)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		switch {
		case num == elemContent && typ == protowire.BytesType:
			out = protowire.AppendTag(out, num, typ)
			out = protowire.AppendString(out, text)
		case num == elemMidHash && typ == protowire.BytesType:
			out = protowire.AppendTag(out, num, typ)
			out = protowire.AppendString(out, "00000000")
		default:
			out = append(out, b[:n]...)
		}
		b = b[n:]
	}
	return out, nil
}
