package bilibili

import (
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
	apiURL    = "https://api.bilibili.com"
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36"
	referer   = "https://www.bilibili.com"

	requestsPerSecond = 3                // 全局令牌桶
	requestTimeout    = 10 * time.Second // 单个请求的超时，超时后照常重试；整次拉取的总时限由调用方的 ctx 决定
	maxRetries        = 3                // rate_limited 与 upstream 最多重试的次数
	firstRetryDelay   = 500 * time.Millisecond
	maxRetryDelay     = 4 * time.Second
	maxBodySize       = 32 << 20 // 响应体的上限，超过时按 Upstream 处理，不截断（截断的 protobuf 可能在弹幕边界上解出部分弹幕）
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
	http       *http.Client
	baseURL    string        // API 的地址，测试里换成 httptest 服务器
	limiter    *rate.Limiter // 全局令牌桶，所有绑定共用
	retryDelay time.Duration // 第一次重试前的等待，之后每次翻倍
}

func newClient() *client {
	return &client{
		http:       &http.Client{Timeout: requestTimeout},
		baseURL:    apiURL,
		limiter:    rate.NewLimiter(requestsPerSecond, 1),
		retryDelay: firstRetryDelay,
	}
}

// reply 一次请求的响应，状态码只会是 200 或 304。
type reply struct {
	status int
	isJSON bool // Content-Type 是 JSON：对二进制接口来说是 B 站的错误
	body   []byte
}

// get 发一次 GET（target 为路径加查询串），不重试。状态码不是 200、304 时返回归类后的错误：412 为 RateLimited，其余为 Upstream。
func (c *client) get(ctx context.Context, target string) (reply, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return reply{}, sourceError(source.Upstream, fmt.Errorf("GET %s: %w", target, err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+target, nil)
	if err != nil {
		return reply{}, fmt.Errorf("GET %s: %w", target, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", referer)

	resp, err := c.http.Do(req)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err // 不重复完整的 URL
		}
		return reply{}, sourceError(source.Upstream, fmt.Errorf("GET %s: %w", target, err))
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNotModified:
	case http.StatusPreconditionFailed:
		return reply{}, sourceError(source.RateLimited, fmt.Errorf("GET %s: %s", target, resp.Status))
	default:
		return reply{}, sourceError(source.Upstream, fmt.Errorf("GET %s: %s", target, resp.Status))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	switch {
	case err != nil:
		return reply{}, sourceError(source.Upstream, fmt.Errorf("GET %s: read body: %w", target, err))
	case len(body) > maxBodySize:
		return reply{}, sourceError(source.Upstream, fmt.Errorf("GET %s: response too large", target))
	}
	return reply{
		status: resp.StatusCode,
		isJSON: strings.Contains(resp.Header.Get("Content-Type"), "json"),
		body:   body,
	}, nil
}

// getJSON 请求 JSON 接口，code 为 0 时把 data 解进 out。
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

// decodeEnvelope 解析 B 站 JSON 接口的统一结构 {code, message, data}，返回 data。
// code 不为 0、或 data 里带 v_voucher（要求风控验证）时返回归类后的错误；不是这个结构时为 Upstream。
func decodeEnvelope(target string, body []byte) (json.RawMessage, error) {
	var env struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, sourceError(source.Upstream, fmt.Errorf("GET %s: decode response: %w", target, err))
	}
	if env.Code != 0 {
		return nil, sourceError(codeKind(env.Code), fmt.Errorf("GET %s: code %d %s", target, env.Code, env.Message))
	}
	var risk struct {
		VVoucher string `json:"v_voucher"`
	}
	if json.Unmarshal(env.Data, &risk) == nil && risk.VVoucher != "" {
		return nil, sourceError(source.RateLimited, fmt.Errorf("GET %s: v_voucher", target))
	}
	return env.Data, nil
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
