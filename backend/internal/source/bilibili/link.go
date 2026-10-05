package bilibili

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	linkHosts      = []string{"www.bilibili.com", "bilibili.com", "m.bilibili.com"}
	spaceHost      = "space.bilibili.com"              // UP 主的个人空间：合集页、系列页
	shortLinkHosts = []string{"b23.tv", "bili2233.cn"} // 只对这两个域名跟随跳转
)

// linkIDs 长链接里 ID 所在的路径，以及这个位置上可以出现的 ID 前缀。
var linkIDs = map[string][]string{
	"/video/":         {"BV", "av"},
	"/bangumi/play/":  {"ep", "ss"},
	"/bangumi/media/": {"md"},
}

// bareIDs 裸 ID 可以是的前缀。
var bareIDs = []string{"BV", "av", "ep", "ss", "md"}

// target 链接指向的页面，由 parseURL 只做字符串解析得到。集面板与季面板各自决定接受哪些。
type target struct {
	kind string // 见下面的常量；为空表示认不出
	id   int64  // aid、ep_id、season_id、media_id 或投稿合集的 ID；系列与 lists 为 0
	page int    // 投稿的分 P，从 1 开始；其他为 0

	short, to string // 由短链跳转而来时：短链和跳转到的地址
}

// unsupported 认不出、或者这个面板不接受的链接：由短链跳转而来的为 InvalidLink，提示为 message
// （是 B 站的短链，只是指向了绑定不了的页面，例如直播间、个人空间）；其他的为 source.ErrUnrecognized。
func (t target) unsupported(message string) error {
	if t.short == "" {
		return source.ErrUnrecognized
	}
	return &source.Error{Kind: source.InvalidLink, Message: message, Err: fmt.Errorf("%s 跳转到 %s", t.short, t.to)}
}

const (
	targetVideo     = "video"     // 投稿，可带分 P
	targetEpisode   = "episode"   // 番剧单集 ep
	targetSeason    = "season"    // 番剧的一季 ss
	targetMedia     = "media"     // 番剧作品页 md
	targetUGCSeason = "ugcSeason" // 空间里的投稿合集页
	targetSeries    = "series"    // 空间里的系列页、系列的播放列表
	targetLists     = "lists"     // 空间里没写 type 的列表页：分不出是合集还是系列（两者的 ID 是两套编号）
)

// toURL 把用户贴的文本解析成 URL；没写协议的链接（例如 www.bilibili.com/video/BV…、b23.tv/…）补上 https://。
// 裸 ID（例如 BV…?p=2）没有协议和域名，原样留在 Path 和查询串里。
func toURL(link string) (*url.URL, bool) {
	link = strings.TrimSpace(link)
	u, err := url.Parse(link)
	if err == nil && u.Scheme == "" && strings.Contains(u.Path, "/") {
		u, err = url.Parse("https://" + link)
	}
	return u, err == nil
}

// shortLink u 是 b23.tv、bili2233.cn 的短链时，返回请求它用的地址：只留域名和路径，查询串对短链没有意义。
func shortLink(u *url.URL) (string, bool) {
	host := strings.ToLower(u.Hostname())
	if (u.Scheme != "https" && u.Scheme != "http") || !slices.Contains(shortLinkHosts, host) || strings.Trim(u.Path, "/") == "" {
		return "", false
	}
	return "https://" + host + u.EscapedPath(), true
}

// resolve 解析用户贴的链接：短链请求一次、只读 Location，按跳转到的长链接解析，不跟随第二次跳转；其他链接不联网。
// 认不出时 kind 为空，由调用方交给 target.unsupported；只有请求短链失败时返回错误。
func (a *Adapter) resolve(ctx context.Context, link string) (target, error) {
	u, ok := toURL(link)
	if !ok {
		return target{}, nil
	}
	short, ok := shortLink(u)
	if !ok {
		return parseURL(u), nil
	}
	var to *url.URL
	err := a.client.retry(ctx, func() (err error) {
		to, err = a.client.location(ctx, short)
		return err
	})
	if err != nil {
		return target{}, err
	}
	t := parseURL(to)
	t.short, t.to = short, to.String()
	return t, nil
}

// parseURL 只做字符串解析，不联网，认不出时返回 target{}（kind 为空）：
//   - 投稿 video/BV…、video/av…，可带 ?p=N（从 1 开始，缺省为 1）；番剧单集 bangumi/play/ep…；
//     番剧的一季 bangumi/play/ss…；作品页 bangumi/media/md…；以及裸的 BV、av（同样可带 ?p=）、ep、ss、md 号；
//   - 空间里的合集页 space.bilibili.com/{mid}/lists/{sid}?type=season、旧版 …/channel/collectiondetail?sid=；
//   - 系列页 …/lists/{sid}?type=series、旧版 …/channel/seriesdetail?sid=，系列的播放列表 www.bilibili.com/list/{mid}?sid=；
//   - 没写 type 的 …/lists/{sid}。
func parseURL(u *url.URL) target {
	if u.Scheme == "" && u.Host == "" {
		return parseID(u.Path, bareIDs, u.Query())
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return target{}
	}
	host := strings.ToLower(u.Hostname())
	if host == spaceHost {
		return parseSpaceURL(u)
	}
	if !slices.Contains(linkHosts, host) {
		return target{}
	}
	if mid, ok := strings.CutPrefix(strings.TrimSuffix(u.Path, "/"), "/list/"); ok {
		if isPositive(mid) && isPositive(u.Query().Get("sid")) {
			return target{kind: targetSeries}
		}
		return target{}
	}
	for path, prefixes := range linkIDs {
		if rest, ok := strings.CutPrefix(u.Path, path); ok {
			return parseID(strings.TrimSuffix(rest, "/"), prefixes, u.Query())
		}
	}
	return target{}
}

// parseID 解析一个 ID，它在这个位置上只能以 prefixes 之一开头；投稿的分 P 取自查询串的 p。认不出时返回 target{}。
func parseID(id string, prefixes []string, query url.Values) target {
	if !slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(id, p) }) {
		return target{}
	}
	kinds := map[string]string{"ep": targetEpisode, "ss": targetSeason, "md": targetMedia}
	if kind, ok := kinds[id[:2]]; ok {
		n, ok := positiveInt(id[2:])
		if !ok {
			return target{}
		}
		return target{kind: kind, id: n}
	}

	aid, ok := parseVideoID(id)
	if !ok {
		return target{}
	}
	page := int64(1)
	if p, ok := query["p"]; ok {
		if page, ok = positiveInt(p[0]); !ok {
			return target{}
		}
	}
	return target{kind: targetVideo, id: aid, page: int(page)}
}

// parseSpaceURL 个人空间里的合集页与系列页，路径以 UP 主的 mid 开头。认不出时返回 target{}。
func parseSpaceURL(u *url.URL) target {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 3 || !isPositive(parts[0]) {
		return target{}
	}
	q := u.Query()
	switch {
	case parts[1] == "lists":
		sid, ok := positiveInt(parts[2])
		if !ok {
			return target{}
		}
		switch q.Get("type") {
		case "season":
			return target{kind: targetUGCSeason, id: sid}
		case "series":
			return target{kind: targetSeries}
		case "":
			return target{kind: targetLists}
		}
	case parts[1] == "channel" && parts[2] == "collectiondetail":
		if sid, ok := positiveInt(q.Get("sid")); ok {
			return target{kind: targetUGCSeason, id: sid}
		}
	case parts[1] == "channel" && parts[2] == "seriesdetail" && isPositive(q.Get("sid")):
		return target{kind: targetSeries}
	}
	return target{}
}

// parseVideoID 把 av 号或 BV 号换算成 aid。
func parseVideoID(id string) (int64, bool) {
	if digits, ok := strings.CutPrefix(id, "av"); ok {
		aid, ok := positiveInt(digits)
		return aid, ok && aid < maxAid
	}
	return bvToAid(id)
}

// positiveInt 解析只由数字组成的正整数，不接受正负号和空白。
func positiveInt(s string) (int64, bool) {
	if s == "" || strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// isPositive s 是否是 positiveInt 认得的正整数，只看能否解析、不要值时用。
func isPositive(s string) bool {
	_, ok := positiveInt(s)
	return ok
}

// BV 号与 aid 的公开换算算法：BV 号是 "BV1" 加 9 位 58 进制数，aid 小于 2^51。
const (
	bvAlphabet = "FcwAPNKTMug3GV5Lj7EJnHpWsx4tb8haYeviqBz6rkCy12mUSDQX9RdoZf"
	bvBase     = int64(len(bvAlphabet))
	bvXor      = 23442827791579
	maxAid     = 1 << 51
)

// aidToBV 先算 (maxAid | aid) ^ bvXor，按 58 进制从末位往前填，再交换下标 3↔9、4↔7。aid 必须在 (0, maxAid) 之间。
func aidToBV(aid int64) string {
	b := []byte("BV1000000000")
	v := (maxAid | aid) ^ bvXor
	for i := len(b) - 1; v > 0; i-- {
		b[i] = bvAlphabet[v%bvBase]
		v /= bvBase
	}
	b[3], b[9] = b[9], b[3]
	b[4], b[7] = b[7], b[4]
	return string(b)
}

// bvToAid aidToBV 的逆运算。任何格式合法的 BV 号都能解出一个数，只有能原样换算回去的才算有效。
func bvToAid(bvid string) (int64, bool) {
	if len(bvid) != len("BV1000000000") || !strings.HasPrefix(bvid, "BV1") {
		return 0, false
	}
	b := []byte(bvid)
	b[3], b[9] = b[9], b[3]
	b[4], b[7] = b[7], b[4]
	var v int64
	for _, c := range b[3:] {
		i := strings.IndexByte(bvAlphabet, c)
		if i < 0 {
			return 0, false
		}
		v = v*bvBase + int64(i)
	}
	aid := (v & (maxAid - 1)) ^ bvXor
	return aid, aid > 0 && aidToBV(aid) == bvid
}
