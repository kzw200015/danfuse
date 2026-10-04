// Package source 源适配器的接口：与某个平台通信，或解析某种本地弹幕文件。
// 适配器只负责请求、限速、重试、解析，不含匹配、缓存和存储决策，不访问数据库。
// 各平台的适配器在子包里（bilibili），由 app 注册进 Registry；业务代码只依赖这里的接口。
package source

import (
	"context"
	"errors"
	"log/slog"
	"slices"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// Ref 弹幕源在平台内的引用，是适配器自己定义的 JSON，存入 bindings.ref（jsonb），
// 参与唯一约束 (episode_id, adapter, ref)。适配器之外的代码不解析它。
type Ref []byte

// Adapter 所有源适配器都有的能力。
type Adapter interface {
	// ID 存入 bindings.adapter，一经发布不能再改。
	ID() string
	// Platform 弹幕所在的平台；没有平台的来源返回 danmaku.PlatformNone。
	Platform() danmaku.Platform
	// Describe 由 ref 生成展示用的来源链接和标签，纯计算，不联网。
	Describe(ref Ref) (Display, error)
	// Fetch 按 ref 重新解析出当前的弹幕源（例如 B 站每次重新取 cid），取标题、时长和全部弹幕。
	// 全有或全无：任何一个请求最终失败，整次返回 *Error，不返回部分弹幕。
	// 重试已在内部做完；总时限由调用方的 ctx 决定，超时也返回 Kind 为 Upstream 的 *Error。
	Fetch(ctx context.Context, ref Ref) (Fetched, error)
}

// Linker 可选能力：从用户贴的链接得到 ref。
type Linker interface {
	// ParseLink 识别链接并返回规范化的 ref：同一个弹幕源不论链接怎么写，ref 都相同。
	// 不是本平台的链接返回 ErrUnrecognized；是本平台的但不能绑定（例如整季的链接）返回 Kind 为 InvalidLink 的 *Error。
	// 可能联网，例如跟随短链跳转。
	ParseLink(ctx context.Context, link string) (Ref, error)
}

// Display 绑定在管理界面上的来源展示。
type Display struct {
	URL   string // 例如 https://www.bilibili.com/video/BV1xx411c7XX?p=2
	Label string // 例如"B 站投稿 BV1xx411c7XX P2"
}

// Fetched 一次拉取的结果。
type Fetched struct {
	Title    string // 弹幕源的标题，例如 B 站投稿为"视频标题 / 分 P 标题"
	Duration int    // 弹幕源视频的时长，秒
	Danmaku  []danmaku.Danmaku
	// LogAttrs 适配器自己的统计（例如 B 站的 protobuf 条数、XML 条数和两者的交集），由调用方连同新增条数记一条 info 日志。
	LogAttrs []slog.Attr
}

// Kind 适配器错误的类别，调用方据此决定绑定的状态和 HTTP 状态码。
type Kind int

const (
	InvalidLink  Kind = iota + 1 // 链接无法识别，或能识别但不能绑定
	NotFound                     // 弹幕源不存在、已删除或不可见；重新拉取时绑定标为失效
	AuthRequired                 // 需要登录
	RateLimited                  // 平台限流
	Upstream                     // 平台故障、超时、响应无法解析
)

// Error 适配器返回的错误。Message 是给用户看的提示，由适配器写，因为提示与平台有关
// （例如"B 站限流，请稍后再试"）；Err 是底层原因（平台的错误码等），只进日志。
// 调用方用 errors.AsType 取出。
type Error struct {
	Kind    Kind
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// ErrUnrecognized Linker 不认识这个链接，Registry 会接着交给下一个适配器。
var ErrUnrecognized = errors.New("source: unrecognized link")

// Registry 已注册的源适配器。
type Registry struct {
	adapters []Adapter
}

// NewRegistry 按给定顺序注册，ParseLink 也按这个顺序询问。
func NewRegistry(adapters ...Adapter) *Registry {
	return &Registry{adapters: adapters}
}

// Get 按 bindings.adapter 取适配器。
func (r *Registry) Get(id string) (Adapter, bool) {
	i := slices.IndexFunc(r.adapters, func(a Adapter) bool { return a.ID() == id })
	if i < 0 {
		return nil, false
	}
	return r.adapters[i], true
}

// ParseLink 依次交给实现了 Linker 的适配器，返回第一个认识这个链接的适配器和规范化的 ref。
// 适配器返回 ErrUnrecognized 以外的错误时直接返回；都不认识时返回 Kind 为 InvalidLink 的 *Error（"无法识别的链接"）。
func (r *Registry) ParseLink(ctx context.Context, link string) (Adapter, Ref, error) {
	for _, a := range r.adapters {
		linker, ok := a.(Linker)
		if !ok {
			continue
		}
		ref, err := linker.ParseLink(ctx, link)
		switch {
		case errors.Is(err, ErrUnrecognized):
			continue
		case err != nil:
			return nil, nil, err
		}
		return a, ref, nil
	}
	return nil, nil, &Error{Kind: InvalidLink, Message: "无法识别的链接"}
}
