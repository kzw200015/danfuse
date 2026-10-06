package bilibili

// live 模式：请求真实的 B 站，确认适配器在一份固定的公开视频列表（集面板的 liveCases、季面板的 liveCollectionCases）上
// 仍然正确。默认跳过，CI 不请求 B 站。
//
//	go test ./internal/source/bilibili -run TestLive -args -live           # 只验证
//	go test ./internal/source/bilibili -run TestLive -args -live -update   # 验证，并重新录制 testdata/
//
// 在里程碑验收时和改动 B 站适配器之后各跑一遍。全部用例共 36 次请求（结束时打印实际次数），靠适配器自己的令牌桶限速。
// 不带 SESSDATA，以未登录的身份请求；港澳台限定番剧的用例要求从大陆请求。
// 列表里的视频状态变了（被删、开关弹幕、改标题）就换一个，再加 -update 重新录制。
//
// -update 只在 -live 时生效：先清空 testdata/，再把 B 站的响应脱敏后写进去，原始响应不落盘；
// 限流、接口异常这类出错的响应不录制，原样交给适配器重试。
//   - view、pgc（含按 season_id 取整季）、md 换算、合集条目列表：只保留适配器会读的字段；
//     标记了 redactTitle 的视频，标题也换成占位文本；
//   - seg.so：解码后每段只留前 sampleElems 条；正文依次换成"弹幕1""弹幕2"……，每段的前几条换成 specialTexts；
//     发送者哈希换成固定值；ID、时间、模式、颜色等其余字段和其他顶层字段原样保留，再重新编码；
//   - XML：解压后同样只留前 sampleElems 条，正文与发送者哈希的处理同上，存成便于阅读的 .xml，回放时再压缩；
//   - 短链：只存跳转的目标，存成 .302。
//
// TestSamples 平时回放这些样本，断言与 live 模式相同，另外检查特殊正文原样保留。

import (
	"bytes"
	"compress/flate"
	"encoding/json"
	"encoding/xml"
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
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/danmaku/bilifmt"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	live   = flag.Bool("live", false, "TestLive 请求真实的 B 站；不加时跳过")
	update = flag.Bool("update", false, "与 -live 一起用：把 B 站的响应脱敏后写进 testdata/，覆盖原有样本")
)

// liveCases 固定的公开视频列表。挑的都是多年不变、内容中性的视频：官方账号的 MV、2012 年的多 P 老投稿、
// 一直不可见的 av1、从未存在的 aid、长期关闭弹幕的官方视频，以及几部普通的日常、运动题材动画。
var liveCases = []struct {
	name        string
	link        string
	ref         string      // 期望的规范化 ref；为空表示链接被拒绝
	kind        source.Kind // 不为 0 时期望这一类错误：InvalidLink 来自 ParseLink，其余来自 Fetch
	message     string      // 不为空时期望错误的提示是它
	parseOnly   bool        // 只解析链接：ref 与别的用例相同，不重复拉取
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
	{
		name: "番剧 ep 链接", link: "https://www.bilibili.com/bangumi/play/ep231951",
		ref:   `{"kind":"episode","epId":231951}`,
		title: "前进吧！登山少女 第三季 第1话 在筑波山上的第一次约会！？", duration: 811,
	},
	{
		name: "b23.tv 短链", link: "https://b23.tv/ep231951",
		ref:       `{"kind":"episode","epId":231951}`,
		parseOnly: true,
	},
	{
		// 番剧的稿件：两个分 P 是 section 里的两个 PV，redirect_url 指向第一个，P2 按 cid 找到第二个
		name: "带 redirect_url 的投稿", link: "https://www.bilibili.com/video/av542213531?p=2",
		ref:   `{"kind":"video","aid":542213531,"page":2}`,
		title: "攀岩少女！ 正式PV", duration: 132,
	},
	{
		// 一部仅限港澳台地区的普通动画（md28234730），从大陆请求 pgc 时与不存在的 ep 一样返回 -404
		name: "港澳台限定番剧", link: "https://www.bilibili.com/bangumi/play/ep409059",
		ref:     `{"kind":"episode","epId":409059}`,
		kind:    source.NotFound,
		message: "视频不存在、已删除或不可见",
	},
	{
		name: "整季的 ss 链接被拒绝", link: "https://www.bilibili.com/bangumi/play/ss24605",
		kind: source.InvalidLink, message: "整季或合集的链接请在季面板绑定",
	},
	{
		name: "作品页的 md 链接被拒绝", link: "https://www.bilibili.com/bangumi/media/md28237119",
		kind: source.InvalidLink, message: "整季或合集的链接请在季面板绑定",
	},
}

// liveCollectionCases 季面板的固定链接：一部完结的番剧（混有预告）、一部连载中的番剧、一个投稿合集（每个稿件都有多个分 P）、
// 一个多 P 又属于这个合集的投稿，以及被拒绝的系列、单 P 稿件。连载中的番剧完结了、合集被改了就换一个，再重新录制。
// 每个用例只列出第一个候选。
var liveCollectionCases = []struct {
	name       string
	link       string
	candidates []string    // 期望的候选，即合集 ref，按顺序；为空表示链接被拒绝
	kind       source.Kind // 链接被拒绝时期望的错误类别
	message    string
	title      string // 第一个候选的合集标题
	finished   bool
	minItems   int    // 条目至少这么多：连载中的会越来越多
	exact      bool   // 条目恰好 minItems 条
	first      string // 按内置的集号规则认出序号之后，第一个条目的"序号|标签|对不上的原因"
}{
	{
		// 完结的番剧：episodes 里混着 10 个预告，与正片同号，不是条目
		name: "完结的番剧：作品页 md 链接", link: "https://www.bilibili.com/bangumi/media/md28237119",
		candidates: []string{`{"kind":"bangumi","seasonId":41410}`},
		title:      "间谍过家家", finished: true, minItems: 25, exact: true, first: "1|第1话 任务1 <枭>行动|",
	},
	{
		name: "连载中的番剧：单集 ep 链接", link: "https://www.bilibili.com/bangumi/play/ep1553970",
		candidates: []string{`{"kind":"bangumi","seasonId":92458}`},
		title:      "宝可梦 地平线（中配）", minItems: 45, first: "1|第1话 起源的吊坠 前篇|",
	},
	{
		name: "投稿合集：新版合集页", link: "https://space.bilibili.com/50329118/lists/8597253?type=season",
		candidates: []string{`{"kind":"ugcSeason","seasonId":8597253,"mid":50329118}`},
		title:      "2026EWC", minItems: 31, first: "0|【2026EWC】7月16日 MIBR.LOS vs JDG / 第一局|名称里认不出集号",
	},
	{
		name: "多 P 又属于合集的投稿", link: "https://www.bilibili.com/video/BV1kcK568Edu",
		candidates: []string{`{"kind":"multiPage","aid":116930132313786}`, `{"kind":"ugcSeason","seasonId":8597253,"mid":50329118}`},
		title:      "【2026EWC】7月16日 DK vs G2", minItems: 5, exact: true, first: "0|【2026EWC】7月16日 DK vs G2 / 第一局|名称里认不出集号",
	},
	{
		name: "系列页被拒绝", link: "https://space.bilibili.com/37737161/lists/2800550?type=series",
		kind: source.InvalidLink, message: "暂不支持系列",
	},
	{
		name: "单 P 且不属于合集的稿件被拒绝", link: "https://www.bilibili.com/video/BV1GJ411x7h7/",
		kind: source.InvalidLink, message: "这个稿件只有一个分 P，也不属于合集，请在集面板绑定",
	},
}

func TestLive(t *testing.T) {
	if !*live {
		t.Skip("live 模式默认关闭，用 -args -live 打开")
	}
	a := New(config.Bilibili{RequestsPerSecond: 3}) // 与默认值一致
	if *update {
		a.client.http.Transport = recordFake(t).transport()
	}
	requests := countRequests(a)
	checkCases(t, a, requests)
	checkCollectionCases(t, a)
	t.Logf("共请求 B 站 %d 次", requests())
}

// TestSanitizeXML 录制时的 XML 脱敏：录好的样本再脱敏一次保持不变（包括没有弹幕的）；有认不出的 <d> 时不录制。
func TestSanitizeXML(t *testing.T) {
	paths, err := filepath.Glob("testdata/xml-*.xml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("没有 XML 样本：%v", err)
	}
	for _, path := range paths {
		doc, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := sanitizeXML(doc); err != nil || !bytes.Equal(got, doc) {
			t.Errorf("%s 再脱敏一次变了：%v", path, err)
		}
	}
	if _, err := sanitizeXML(xmlDoc(`<d p="1,1,25,0,0,0,abcdef12,1,10">认得出</d>`, `<d p='1,1,25,0,0,0,abcdef12,2,10'>认不出</d>`)); err == nil {
		t.Error("有认不出的 <d> 时应拒绝录制")
	}
}

func TestSamples(t *testing.T) {
	a := replayFake(t).adapter()
	results := checkCases(t, a, countRequests(a))
	checkCollectionCases(t, a)

	// 样本每段只留前 sampleElems 条，都是普通的文字弹幕，一条不少
	for _, name := range []string{"单 P 投稿的 BV 链接", "多 P 投稿带 ?p=3", "av 链接"} {
		if n := logAttr(results[name], "protobuf"); n != sampleElems {
			t.Errorf("%s：protobuf %d 条，want %d", name, n, sampleElems)
		}
	}
	// 已知弹幕的全部字段，包括缺省为 0 的时间和颜色；protobuf 的在前，XML 独有的接在后面
	for _, tt := range []struct {
		name  string
		index int
		want  danmaku.Danmaku
	}{
		{"单 P 投稿的 BV 链接", 170, danmaku.Danmaku{SourceID: 44722872605212679, TimeMs: 0, Mode: danmaku.ModeBottom, Color: 0xE70012, Text: "弹幕171"}},
		{"多 P 投稿带 ?p=3", 95, danmaku.Danmaku{SourceID: 869452471, TimeMs: 120642, Mode: danmaku.ModeBottom, Color: 0, Text: "弹幕96"}},
		// XML 独有的：protobuf 3 段共 473 条之后；时间由秒换算成毫秒
		{"番剧 ep 链接", 474, danmaku.Danmaku{SourceID: 43856088912625669, TimeMs: 701035, Mode: danmaku.ModeTop, Color: 0xFFAA02, Text: `&<>"' 转义`}},
		{"带 redirect_url 的投稿", 12, danmaku.Danmaku{SourceID: 38749444289069063, TimeMs: 95234, Mode: danmaku.ModeScroll, Color: 0xFFFFFF, Text: "😀🎉 emoji"}},
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

// checkCases 逐个解析、拉取 liveCases 并断言，返回拉取成功的结果。requests 返回适配器已发出的请求数。
func checkCases(t *testing.T, a *Adapter, requests func() int) map[string]source.Fetched {
	t.Helper()
	results := map[string]source.Fetched{}
	registry := source.NewRegistry(a)
	for _, tc := range liveCases {
		t.Run(tc.name, func(t *testing.T) {
			before := requests()
			_, ref, err := registry.ParseLink(t.Context(), tc.link)
			if tc.kind == source.InvalidLink {
				assertErrorMessage(t, err, tc.kind, tc.message)
				if n := requests() - before; n != 0 {
					t.Errorf("被拒绝的链接不应请求上游，请求了 %d 次", n)
				}
				return
			}
			if err != nil || string(ref) != tc.ref {
				t.Fatalf("ParseLink(%q) = (%s, %v), want %s", tc.link, ref, err, tc.ref)
			}
			if tc.parseOnly {
				return
			}

			got, err := a.Fetch(t.Context(), ref)
			if tc.kind != 0 {
				if tc.message != "" {
					assertErrorMessage(t, err, tc.kind, tc.message)
				} else {
					assertError(t, err, tc.kind)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			title := got.Title
			if tc.redactTitle {
				title = redactedTitle // 输出里同样不出现与本项目无关的标题
			}
			t.Logf("%q，%d 秒，%d 条弹幕，%v", title, got.Duration, len(got.Danmaku), got.LogAttrs)
			titleOK := got.Title == tc.title || tc.redactTitle && got.Title != ""
			if !titleOK || got.Duration != tc.duration {
				t.Errorf("Fetch() = (%q, %d 秒), want (%q, %d 秒)", title, got.Duration, tc.title, tc.duration)
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
			// 合并后的条数 = protobuf + XML - 交集；没关弹幕的视频两边都有弹幕
			proto, xml, overlap := logAttr(got, "protobuf"), logAttr(got, "xml"), logAttr(got, "overlap")
			if proto+xml-overlap != len(got.Danmaku) || overlap > min(proto, xml) || !tc.closed && (proto == 0 || xml == 0) {
				t.Errorf("LogAttrs = %v，与合并后的 %d 条对不上", got.LogAttrs, len(got.Danmaku))
			}
			results[tc.name] = got
		})
	}
	return results
}

// checkCollectionCases 逐个识别 liveCollectionCases 的链接并断言候选，列出第一个候选并断言合集。
func checkCollectionCases(t *testing.T, a *Adapter) {
	t.Helper()
	registry := source.NewRegistry(a)
	for _, tc := range liveCollectionCases {
		t.Run(tc.name, func(t *testing.T) {
			_, candidates, err := registry.ParseCollectionLink(t.Context(), tc.link)
			if tc.candidates == nil {
				assertErrorMessage(t, err, tc.kind, tc.message)
				return
			}
			var refs []string
			for _, c := range candidates {
				refs = append(refs, string(c.Ref))
			}
			if err != nil || !slices.Equal(refs, tc.candidates) {
				t.Fatalf("ParseCollectionLink(%q) = (%q, %v), want %q", tc.link, refs, err, tc.candidates)
			}

			got, err := a.ListCollection(t.Context(), candidates[0].Ref)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%q，完结 %v，%d 条", got.Title, got.Finished, len(got.Items))
			if got.Title != tc.title || got.Finished != tc.finished {
				t.Errorf("ListCollection() = (%q, 完结 %v), want (%q, %v)", got.Title, got.Finished, tc.title, tc.finished)
			}
			if n := len(got.Items); n < tc.minItems || tc.exact && n != tc.minItems {
				t.Errorf("%d 条，want %d 条", n, tc.minItems)
			}
			seen := map[string]bool{}
			for _, it := range got.Items {
				if _, err := decodeRef(it.Ref); err != nil || seen[string(it.Ref)] {
					t.Errorf("条目的弹幕源 ref 不合法或重复：%+v", it)
				}
				seen[string(it.Ref)] = true
				if it.Label == "" || !got.NumberedByRule && it.Unmatched == "" && it.Number < 1 {
					t.Errorf("条目没有标签或序号：%+v", it)
				}
			}
			// 按内置的集号规则认出序号之后再看第一个条目，与季绑定预览看到的相同
			if items := source.NumberItems(got, source.EpisodeRule{}); len(items) > 0 {
				first := items[0]
				if s := fmt.Sprintf("%d|%s|%s", first.Number, first.Label, first.Unmatched); s != tc.first {
					t.Errorf("第一个条目 = %s, want %s", s, tc.first)
				}
			}
		})
	}
}

// logAttr 取 LogAttrs 里的一个计数，没有时为 -1。
func logAttr(f source.Fetched, key string) int {
	i := slices.IndexFunc(f.LogAttrs, func(a slog.Attr) bool { return a.Key == key })
	if i < 0 {
		return -1
	}
	return int(f.LogAttrs[i].Value.Int64())
}

// countRequests 给适配器的 HTTP 客户端套上计数，返回读取已发出请求数的函数。
func countRequests(a *Adapter) func() int {
	base := a.client.http.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	var n atomic.Int64
	a.client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		n.Add(1)
		return base.RoundTrip(r)
	})
	return func() int { return int(n.Load()) }
}

const sampleElems = 200 // 每段、每份 XML 样本最多保留的弹幕条数

// specialTexts 样本每段、每份 XML 前几条弹幕的正文，覆盖 emoji、需要转义的字符和换行。
var specialTexts = []string{"😀🎉 emoji", `&<>"' 转义`, "第一行\n第二行"}

// sampleText 样本里第 n 条（从 1 开始）弹幕的正文。
func sampleText(n int) string {
	if n <= len(specialTexts) {
		return specialTexts[n-1]
	}
	return fmt.Sprintf("弹幕%d", n)
}

// redactedTitle 脱敏后的视频标题与分 P 标题，见 liveCases 的 redactTitle。
const redactedTitle = "（标题已脱敏）"

// redactedMidHash 发送者 mid 的哈希（可以反查用户）在样本里换成的固定值。
const redactedMidHash = "00000000"

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
	client := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	return startFake(t, func(name string, r *http.Request) (response, bool) {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://"+r.Host+r.URL.RequestURI(), nil)
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
		got := response{
			status:          resp.StatusCode,
			contentType:     resp.Header.Get("Content-Type"),
			contentEncoding: resp.Header.Get("Content-Encoding"),
			location:        resp.Header.Get("Location"),
			body:            body,
		}

		var ext string
		var sample []byte
		switch kind, _, _ := strings.Cut(name, "-"); {
		case slices.Contains([]string{"view", "pgc", "season", "media", "archives"}, kind) && recordableJSON(got):
			ext = ".json"
			switch kind {
			case "view":
				sample, err = sanitizeView(body, redacted[name])
			case "pgc", "season":
				sample, err = sanitizePgc(body)
			case "media":
				sample, err = sanitizeMedia(body)
			default:
				sample, err = sanitizeArchives(body)
			}
			got.body = sample
		case kind == "seg" && got.status == http.StatusNotModified:
			ext, got.body = ".304", nil
		case kind == "seg" && got.status == http.StatusOK && !strings.Contains(got.contentType, "json"):
			ext = ".bin"
			sample, err = sanitizeSegment(body)
			got.body = sample
		case kind == "xml" && got.status == http.StatusOK && got.contentEncoding == "deflate":
			ext = ".xml"
			if sample, err = io.ReadAll(flate.NewReader(bytes.NewReader(body))); err == nil {
				sample, err = sanitizeXML(sample)
				got = xmlResponse(t, sample)
			}
		case kind == "short" && got.status == http.StatusFound && got.location != "":
			ext, sample = ".302", []byte(got.location)
		default:
			// 限流等出错的响应不录制，交给适配器照常重试
			t.Logf("录制 %s：B 站返回 %s %s %.200s，不写入样本", name, resp.Status, got.contentType, body)
			return got, true
		}
		if err != nil {
			t.Errorf("脱敏 %s：%v", name, err)
			return response{status: http.StatusBadGateway}, true
		}
		if err := os.WriteFile(filepath.Join("testdata", name+ext), sample, 0o644); err != nil {
			t.Error(err)
		}
		return got, true
	})
}

// recordableJSON JSON 接口的响应能否录制：成功，或是 NotFound、AuthRequired 这类确定的结果；
// 限流（包括要求风控验证的 v_voucher）和接口异常不录制。
func recordableJSON(got response) bool {
	if got.status != http.StatusOK {
		return false
	}
	_, err := decodeEnvelope("", got.body)
	srcErr, ok := errors.AsType[*source.Error](err)
	return err == nil || ok && (srcErr.Kind == source.NotFound || srcErr.Kind == source.AuthRequired)
}

// sanitizeView view 的 JSON 只保留适配器会读的字段。redactTitle 时把视频标题和分 P 标题换成 redactedTitle。
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
	return encodeSample(v)
}

// sanitizePgc pgc 的 JSON 只保留适配器会读的字段。
func sanitizePgc(body []byte) ([]byte, error) {
	var v struct {
		Code    int         `json:"code"`
		Message string      `json:"message"`
		Result  *seasonData `json:"result,omitempty"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	return encodeSample(v)
}

// sanitizeMedia md 换算的 JSON 只保留 season_id。
func sanitizeMedia(body []byte) ([]byte, error) {
	type media struct {
		SeasonID int64 `json:"season_id"`
	}
	var v struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  *struct {
			Media media `json:"media"`
		} `json:"result,omitempty"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	return encodeSample(v)
}

// sanitizeArchives 合集条目列表的 JSON 只保留适配器会读的字段。
func sanitizeArchives(body []byte) ([]byte, error) {
	var v struct {
		Code    int           `json:"code"`
		Message string        `json:"message"`
		Data    *archivesData `json:"data,omitempty"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	return encodeSample(v)
}

// encodeSample 编码成便于阅读的 JSON 样本：缩进、中文与 & < > 不转义。
func encodeSample(v any) ([]byte, error) {
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
		_, _, tagLen := protowire.ConsumeTag(raw)
		elem, _ := protowire.ConsumeBytes(raw[tagLen:])
		sanitized, err := sanitizeElem(elem, sampleText(kept))
		if err != nil {
			return nil, err
		}
		out = protowire.AppendTag(out, replyElems, protowire.BytesType)
		out = protowire.AppendBytes(out, sanitized)
	}
	return out, nil
}

// elemMidHash DanmakuElem 里发送者 mid 的哈希。
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
			out = protowire.AppendString(out, redactedMidHash)
		default:
			out = append(out, b[:n]...)
		}
		b = b[n:]
	}
	return out, nil
}

// sanitizeXML 脱敏一份 XML 弹幕：保留第一条 <d> 之前的头部，只留前 sampleElems 条 <d>（只留 p 属性），
// p 的第 7 项（发送者哈希）换成固定值，正文按 sampleText 替换；p 的其余各项原样保留。
// 有认不出的 <d> 时不录制：原样写进样本就可能带着没脱敏的正文和发送者哈希。
func sanitizeXML(doc []byte) ([]byte, error) {
	elems := bilifmt.XMLElem.FindAllSubmatchIndex(doc, -1)
	if n := bytes.Count(doc, []byte("<d ")); n != len(elems) {
		return nil, fmt.Errorf("XML 里有 %d 个 <d>，只认出 %d 条，不录制", n, len(elems))
	}
	head := bytes.LastIndex(doc, []byte("</i>")) // 没有弹幕时，头部是 </i> 之前的全部
	if head < 0 {
		return nil, errors.New("XML 不完整，不录制")
	}
	if len(elems) > 0 {
		head = elems[0][0]
	}
	out := bytes.NewBuffer(slices.Clone(doc[:head]))
	for n, m := range elems[:min(len(elems), sampleElems)] {
		p := strings.Split(string(doc[m[2]:m[3]]), ",")
		if len(p) < 8 {
			return nil, fmt.Errorf("p 的项数不对：%q", doc[m[0]:m[1]])
		}
		p[6] = redactedMidHash
		fmt.Fprintf(out, `<d p="%s">`, strings.Join(p, ","))
		if err := xml.EscapeText(out, []byte(sampleText(n+1))); err != nil {
			return nil, err
		}
		out.WriteString("</d>")
	}
	out.WriteString("</i>")
	return out.Bytes(), nil
}
