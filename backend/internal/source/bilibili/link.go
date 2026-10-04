package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/source"
)

var (
	linkHosts      = []string{"www.bilibili.com", "bilibili.com", "m.bilibili.com"}
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

// parseShortLink 请求一次短链，只读 Location，按跳转到的长链接解析；不跟随第二次跳转。
func (a *Adapter) parseShortLink(ctx context.Context, short string) (source.Ref, error) {
	var target *url.URL
	err := a.client.retry(ctx, func() (err error) {
		target, err = a.client.location(ctx, short)
		return err
	})
	if err != nil {
		return nil, err
	}
	v, err := parseURL(target)
	if errors.Is(err, source.ErrUnrecognized) {
		// 是 B 站的短链，只是指向直播间、个人空间这类绑定不了的页面
		return nil, &source.Error{Kind: source.InvalidLink, Message: "短链指向的不是投稿或番剧单集", Err: fmt.Errorf("%s 跳转到 %s", short, target)}
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// parseURL 只做字符串解析，不联网：
//   - 投稿 video/BV…、video/av…，可带 ?p=N（从 1 开始，缺省为 1）；
//   - 番剧单集 bangumi/play/ep…；
//   - 裸的 BV、av 号（同样可带 ?p=）和 ep 号。
//
// 整季 bangumi/play/ss…、作品页 bangumi/media/md… 和裸的 ss、md 号定位不到单集，返回 InvalidLink；
// 其他的返回 source.ErrUnrecognized。
func parseURL(u *url.URL) (ref, error) {
	var id string
	var prefixes []string // 这个位置上可以出现的 ID 前缀
	switch {
	case u.Scheme == "" && u.Host == "":
		id, prefixes = u.Path, bareIDs
	case (u.Scheme == "https" || u.Scheme == "http") && slices.Contains(linkHosts, strings.ToLower(u.Hostname())):
		for path, p := range linkIDs {
			if rest, ok := strings.CutPrefix(u.Path, path); ok {
				id, prefixes = strings.TrimSuffix(rest, "/"), p
			}
		}
	}
	if !slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(id, p) }) {
		return ref{}, source.ErrUnrecognized
	}

	switch prefix, digits := id[:2], id[2:]; prefix {
	case "ep":
		ep, ok := positiveInt(digits)
		if !ok {
			return ref{}, source.ErrUnrecognized
		}
		return ref{Kind: kindEpisode, EpID: ep}, nil
	case "ss", "md":
		if _, ok := positiveInt(digits); !ok {
			return ref{}, source.ErrUnrecognized
		}
		return ref{}, &source.Error{Kind: source.InvalidLink, Message: "请打开具体某一集再复制链接", Err: fmt.Errorf("%s 是整季或作品页", id)}
	}

	aid, ok := parseVideoID(id)
	if !ok {
		return ref{}, source.ErrUnrecognized
	}
	page := int64(1)
	if p, ok := u.Query()["p"]; ok {
		if page, ok = positiveInt(p[0]); !ok {
			return ref{}, source.ErrUnrecognized
		}
	}
	return ref{Kind: kindVideo, Aid: aid, Page: int(page)}, nil
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
