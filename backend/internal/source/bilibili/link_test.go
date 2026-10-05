package bilibili

import (
	"net/http"
	"slices"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/source"
)

// TestParseShortLink 短链请求一次，只读 Location，按跳转到的长链接解析；不跟随第二次跳转。
func TestParseShortLink(t *testing.T) {
	const video, ep = `{"kind":"video","aid":80433022,"page":1}`, `{"kind":"episode","epId":508404}`
	tests := []struct {
		name     string
		link     string
		sample   string // 短链对应的样本名
		resp     response
		want     string // 规范化的 ref；为空时看 kind 与 message
		kind     source.Kind
		message  string
		attempts int // 短链被请求的次数
	}{
		{
			name: "b23.tv 跳到投稿", link: "https://b23.tv/BV1GJ411x7h7", sample: "short-b23.tv-BV1GJ411x7h7",
			resp: redirectResponse("https://www.bilibili.com/video/BV1GJ411x7h7?share_source=copy_web"), want: video, attempts: 1,
		},
		{
			name: "跳到投稿的指定分 P", link: "https://b23.tv/abcdefg", sample: "short-b23.tv-abcdefg",
			resp: redirectResponse("https://m.bilibili.com/video/av80433022?p=2&share_medium=android"), want: `{"kind":"video","aid":80433022,"page":2}`, attempts: 1,
		},
		{
			name: "bili2233.cn 跳到番剧单集", link: "https://bili2233.cn/ep508404", sample: "short-bili2233.cn-ep508404",
			resp: redirectResponse("https://www.bilibili.com/bangumi/play/ep508404"), want: ep, attempts: 1,
		},
		{
			name: "没写协议、带查询串：请求时只留域名和路径", link: " b23.tv/abcdefg?share=1 ", sample: "short-b23.tv-abcdefg",
			resp: redirectResponse("https://www.bilibili.com/video/BV1GJ411x7h7"), want: video, attempts: 1,
		},
		{
			name: "http、域名大写", link: "http://B23.TV/abcdefg", sample: "short-b23.tv-abcdefg",
			resp: response{status: http.StatusMovedPermanently, location: "https://www.bilibili.com/video/BV1GJ411x7h7"}, want: video, attempts: 1,
		},
		{
			name: "失效的短链：200 且没有 Location，不重试", link: "https://b23.tv/pigt3PQ", sample: "short-b23.tv-pigt3PQ",
			resp: response{status: http.StatusOK, contentType: "text/html", body: []byte("<html></html>")},
			kind: source.InvalidLink, message: "短链已失效", attempts: 1,
		},
		{
			name: "跳到整季", link: "https://b23.tv/abcdefg", sample: "short-b23.tv-abcdefg",
			resp: redirectResponse("https://www.bilibili.com/bangumi/play/ss41410"),
			kind: source.InvalidLink, message: "整季或合集的链接请在季面板绑定", attempts: 1,
		},
		{
			name: "跳到空间里的合集页", link: "https://b23.tv/abcdefg", sample: "short-b23.tv-abcdefg",
			resp: redirectResponse("https://space.bilibili.com/2142762/lists/7540520?type=season"),
			kind: source.InvalidLink, message: "整季或合集的链接请在季面板绑定", attempts: 1,
		},
		{
			name: "跳到系列页", link: "https://b23.tv/abcdefg", sample: "short-b23.tv-abcdefg",
			resp: redirectResponse("https://space.bilibili.com/37737161/lists/2800550?type=series"),
			kind: source.InvalidLink, message: "短链指向的不是投稿或番剧单集", attempts: 1,
		},
		{
			name: "跳到绑定不了的页面", link: "https://b23.tv/abcdefg", sample: "short-b23.tv-abcdefg",
			resp: redirectResponse("https://live.bilibili.com/22603245"),
			kind: source.InvalidLink, message: "短链指向的不是投稿或番剧单集", attempts: 1,
		},
		{
			name: "跳到另一个短链：不再跟随", link: "https://b23.tv/abcdefg", sample: "short-b23.tv-abcdefg",
			resp: redirectResponse("https://b23.tv/hijklmn"),
			kind: source.InvalidLink, message: "短链指向的不是投稿或番剧单集", attempts: 1,
		},
		{
			name: "HTTP 412", link: "https://b23.tv/abcdefg", sample: "short-b23.tv-abcdefg",
			resp: statusResponse(http.StatusPreconditionFailed), kind: source.RateLimited, attempts: 4,
		},
		{
			name: "HTTP 502", link: "https://b23.tv/abcdefg", sample: "short-b23.tv-abcdefg",
			resp: statusResponse(http.StatusBadGateway), kind: source.Upstream, attempts: 4,
		},
		{
			name: "跳转没有 Location", link: "https://b23.tv/abcdefg", sample: "short-b23.tv-abcdefg",
			resp: response{status: http.StatusFound}, kind: source.Upstream, attempts: 4,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFake(t, map[string][]response{tt.sample: {tt.resp}})
			a := fake.adapter()

			adapter, ref, err := source.NewRegistry(a).ParseLink(t.Context(), tt.link)

			switch {
			case tt.want != "":
				if err != nil || adapter != a || string(ref) != tt.want {
					t.Errorf("ParseLink() = (%s, %v), want %s", ref, err, tt.want)
				}
			case tt.message != "":
				assertErrorMessage(t, err, tt.kind, tt.message)
			default:
				assertError(t, err, tt.kind)
			}
			// 只请求短链本身，不请求跳转到的地址
			if got := fake.requested(); len(got) != tt.attempts || slices.ContainsFunc(got, func(r string) bool { return r != tt.sample }) {
				t.Errorf("请求 = %q, want %d 次 %s", got, tt.attempts, tt.sample)
			}
		})
	}
}
