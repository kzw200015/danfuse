package bilibili

import (
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"

	"github.com/kzw200015/danfuse/backend/internal/source"
)

const (
	apiURL     = "https://api.bilibili.com"
	commentURL = "https://comment.bilibili.com" // XML 弹幕
	userAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36"
	referer    = "https://www.bilibili.com"

	requestsPerSecond = 3                // 全局令牌桶
	requestTimeout    = 10 * time.Second // 单个请求的超时，超时后照常重试；整次拉取的总时限由调用方的 ctx 决定
	maxRetries        = 3                // rate_limited 与 upstream 最多重试的次数
	firstRetryDelay   = 500 * time.Millisecond
	maxRetryDelay     = 4 * time.Second
	maxBodySize       = 32 << 20 // 响应体（解压后）的上限，超过时按 Upstream 处理，不截断（截断的 protobuf 可能在弹幕边界上解出部分弹幕）
)

// messages 各类错误给用户看的提示。
var messages = map[source.Kind]string{
	source.NotFound:     "视频不存在、已删除或不可见",
	source.AuthRequired: "需要登录，请配置 SESSDATA",
	source.RateLimited:  "B 站限流，请稍后再试",
	source.Upstream:     "B 站接口异常",
}

func sourceError(kind source.Kind, err error) *source.Error {
	return &source.Error{Kind: kind, Message: messages[kind], Err: err}
}

// codeKind B 站业务 code 对应的错误类别，没有列出的都算 Upstream（包括 62004 审核中）。
func codeKind(code int) source.Kind {
	switch code {
	case -404, 62002, 62012: // 不存在或已删除、稿件不可见、仅 UP 主自己可见
		return source.NotFound
	case -403, -101: // 权限不足、未登录
		return source.AuthRequired
	case -412, -352, -401, -509, -799: // 请求被拦截、风控校验失败、非法请求、超出限制、请求过于频繁
		return source.RateLimited
	}
	return source.Upstream
}

// client 对 B 站的所有请求都经过这里：带浏览器 UA 和 Referer，先过全局令牌桶；
// rate_limited 与 upstream 指数退避后重试，最终把 HTTP 状态码和业务 code 归类成 *source.Error。
type client struct {
	http       *http.Client  // 测试里换掉它的 Transport，把请求转给假的 B 站
	sessdata   string        // 可选的登录凭据，只作为 Cookie 发送，不写日志
	limiter    *rate.Limiter // 全局令牌桶，所有绑定共用
	retryDelay time.Duration // 第一次重试前的等待，之后每次翻倍
}

func newClient(sessdata string) *client {
	return &client{
		http: &http.Client{
			Timeout: requestTimeout,
			// 不自动跟随跳转：短链只读 Location 再自己解析，其他接口本来就不跳转
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		sessdata:   sessdata,
		limiter:    rate.NewLimiter(requestsPerSecond, 1),
		retryDelay: firstRetryDelay,
	}
}

// send 发一次 GET，不重试：先过令牌桶，带上浏览器 UA 和 Referer。
// 配置了 SESSDATA 时只发给 bilibili.com 的子域名，与浏览器里这个 Cookie 的作用域一致，短链域名不带。
// 返回的响应由调用方关闭。
func (c *client) send(ctx context.Context, target string) (*http.Response, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, sourceError(source.Upstream, fmt.Errorf("GET %s: %w", target, err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", target, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)
	if c.sessdata != "" && strings.HasSuffix(req.URL.Hostname(), ".bilibili.com") {
		req.AddCookie(&http.Cookie{Name: "SESSDATA", Value: c.sessdata})
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err // 不重复完整的 URL
		}
		return nil, sourceError(source.Upstream, fmt.Errorf("GET %s: %w", target, err))
	}
	return resp, nil
}

// reply 一次请求的响应，状态码只会是 200 或 304。
type reply struct {
	status int
	isJSON bool // Content-Type 是 JSON：对二进制接口来说是 B 站的错误
	body   []byte
}

// get 发一次 GET，不重试，读出解压后的响应体。状态码不是 200、304 时返回归类后的错误，见 statusError。
func (c *client) get(ctx context.Context, target string) (reply, error) {
	resp, err := c.send(ctx, target)
	if err != nil {
		return reply{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotModified {
		return reply{}, statusError(target, resp)
	}

	var body io.Reader = resp.Body
	switch enc := resp.Header.Get("Content-Encoding"); enc {
	case "":
	case "deflate": // XML 弹幕是不带 zlib 头的 raw deflate；net/http 只会自动解开它自己请求的 gzip
		r := flate.NewReader(resp.Body)
		defer r.Close()
		body = r
	default:
		return reply{}, sourceError(source.Upstream, fmt.Errorf("GET %s: unsupported Content-Encoding %q", target, enc))
	}
	b, err := io.ReadAll(io.LimitReader(body, maxBodySize+1))
	switch {
	case err != nil:
		return reply{}, sourceError(source.Upstream, fmt.Errorf("GET %s: read body: %w", target, err))
	case len(b) > maxBodySize:
		return reply{}, sourceError(source.Upstream, fmt.Errorf("GET %s: response too large", target))
	}
	return reply{
		status: resp.StatusCode,
		isJSON: strings.Contains(resp.Header.Get("Content-Type"), "json"),
		body:   b,
	}, nil
}

// location 请求短链，返回它跳转到的地址，不读响应体，不重试。
// 失效的短链返回 200、没有跳转，为 InvalidLink；没有 Location 的跳转为 Upstream；其他状态码见 statusError。
func (c *client) location(ctx context.Context, target string) (*url.URL, error) {
	resp, err := c.send(ctx, target)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return nil, &source.Error{Kind: source.InvalidLink, Message: "短链已失效", Err: fmt.Errorf("GET %s: 200 without redirect", target)}
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		loc, err := resp.Location()
		if err != nil {
			return nil, sourceError(source.Upstream, fmt.Errorf("GET %s: %s: %w", target, resp.Status, err))
		}
		return loc, nil
	default:
		return nil, statusError(target, resp)
	}
}

// statusError 状态码不是期望的值：412 为 RateLimited，其余为 Upstream。
func statusError(target string, resp *http.Response) *source.Error {
	kind := source.Upstream
	if resp.StatusCode == http.StatusPreconditionFailed {
		kind = source.RateLimited
	}
	return sourceError(kind, fmt.Errorf("GET %s: %s", target, resp.Status))
}

// getJSON 请求 JSON 接口，code 为 0 时把 data（pgc 接口为 result）解进 out。
func (c *client) getJSON(ctx context.Context, target string, out any) error {
	return c.retry(ctx, func() error {
		r, err := c.get(ctx, target)
		if err != nil {
			return err
		}
		if r.status != http.StatusOK {
			return sourceError(source.Upstream, fmt.Errorf("GET %s: unexpected status %d", target, r.status))
		}
		data, err := decodeEnvelope(target, r.body)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, out); err != nil {
			return sourceError(source.Upstream, fmt.Errorf("GET %s: decode data: %w", target, err))
		}
		return nil
	})
}

// decodeEnvelope 解析 B 站 JSON 接口的统一结构 {code, message, data}，返回 data；pgc 接口的 data 叫 result。
// code 不为 0、或 data 里带 v_voucher（要求风控验证）时返回归类后的错误；不是这个结构时为 Upstream。
func decodeEnvelope(target string, body []byte) (json.RawMessage, error) {
	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, sourceError(source.Upstream, fmt.Errorf("GET %s: decode response: %w", target, err))
	}
	if env.Code != 0 {
		return nil, sourceError(codeKind(env.Code), fmt.Errorf("GET %s: code %d %s", target, env.Code, env.Message))
	}
	if env.Data == nil {
		env.Data = env.Result
	}
	var risk struct {
		VVoucher string `json:"v_voucher"`
	}
	if json.Unmarshal(env.Data, &risk) == nil && risk.VVoucher != "" {
		return nil, sourceError(source.RateLimited, fmt.Errorf("GET %s: v_voucher", target))
	}
	return env.Data, nil
}

// binaryError 二进制接口（seg.so、XML）返回 JSON 时是 B 站的错误：按错误码归类，但不归为 NotFound。
// cid 不存在时 seg.so 与弹幕已关闭的返回相同，不能用来判断弹幕源是否存在：NotFound 只来自元数据。
func binaryError(target string, body []byte) error {
	_, err := decodeEnvelope(target, body)
	if srcErr, ok := errors.AsType[*source.Error](err); ok && srcErr.Kind == source.NotFound {
		return sourceError(source.Upstream, srcErr.Err)
	}
	if err == nil {
		err = sourceError(source.Upstream, fmt.Errorf("GET %s: unexpected JSON response", target))
	}
	return err
}

// retry 执行 attempt，遇到 RateLimited、Upstream 时退避后重试，最多重试 maxRetries 次；ctx 结束后不再重试。
func (c *client) retry(ctx context.Context, attempt func() error) error {
	for n := 0; ; n++ {
		err := attempt()
		srcErr, ok := errors.AsType[*source.Error](err)
		if !ok || (srcErr.Kind != source.RateLimited && srcErr.Kind != source.Upstream) || n == maxRetries || ctx.Err() != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(c.backoff(n)):
		}
	}
}

// backoff 第 n 次（从 0 起）重试前的等待：从 retryDelay 起指数翻倍，加 ±20% 的随机抖动，不超过 maxRetryDelay。
func (c *client) backoff(n int) time.Duration {
	d := time.Duration(float64(c.retryDelay<<n) * (0.8 + 0.4*rand.Float64()))
	return min(d, maxRetryDelay)
}
