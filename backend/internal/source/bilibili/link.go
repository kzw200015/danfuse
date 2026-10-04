package bilibili

import (
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// videoHosts 投稿链接的域名。
var videoHosts = []string{"www.bilibili.com", "bilibili.com", "m.bilibili.com"}

// parseLink 只做字符串解析，不联网：投稿链接 video/BV…、video/av…（可带 ?p=N，从 1 开始，缺省为 1），
// 以及裸的 BV、av 号（同样可带 ?p=）。不认识时返回 false。
func parseLink(link string) (ref, bool) {
	link = strings.TrimSpace(link)
	u, err := url.Parse(link)
	if err == nil && u.Scheme == "" && strings.Contains(u.Path, "/") {
		u, err = url.Parse("https://" + link) // 没写协议的链接，例如 www.bilibili.com/video/BV…
	}
	if err != nil {
		return ref{}, false
	}

	var id string
	switch {
	case u.Scheme == "" && u.Host == "": // 裸 ID
		id = u.Path
	case (u.Scheme == "https" || u.Scheme == "http") && slices.Contains(videoHosts, strings.ToLower(u.Hostname())):
		rest, ok := strings.CutPrefix(u.Path, "/video/")
		if !ok {
			return ref{}, false
		}
		id = strings.TrimSuffix(rest, "/")
	default:
		return ref{}, false
	}

	aid, ok := parseVideoID(id)
	if !ok {
		return ref{}, false
	}
	page := int64(1)
	if p, ok := u.Query()["p"]; ok {
		if page, ok = positiveInt(p[0]); !ok {
			return ref{}, false
		}
	}
	return ref{Kind: kindVideo, Aid: aid, Page: int(page)}, true
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
