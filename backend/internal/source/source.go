// Package source 源适配器的接口：与某个平台通信，把用户贴的链接认成弹幕源或合集，拉取弹幕、列出合集。
// 适配器只负责请求、限速、重试、解析，不含匹配、缓存和存储决策，不访问数据库。
// 各平台的适配器在子包里（bilibili），由 app 注册进 Registry；业务代码只依赖这里的接口。
package source

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// Ref 弹幕源在平台内的引用，是适配器自己定义的 JSON，存入 bindings.ref（jsonb），
// 参与唯一约束 (episode_id, adapter, ref)。适配器之外的代码不解析它。
type Ref []byte

// Adapter 源适配器，一个平台一个，方法都要实现：弹幕源的三个（ParseLink、Describe、Fetch）和合集的三个
// （ParseCollectionLink、ListCollection、DescribeCollection，类型见 collection.go）。
// 没有合集的平台，ParseCollectionLink 一律返回 ErrUnrecognized，另外两个不会被调用。
// 只适用于按 ref 能重新拉取的弹幕源：弹幕文件没有链接、不能重新拉取，不是这个接口的实现。
type Adapter interface {
	// ID 存入 bindings.adapter、season_bindings.adapter，一经发布不能再改。
	ID() string
	// Platform 弹幕所在的平台；没有平台的弹幕源返回 danmaku.PlatformNone。
	Platform() danmaku.Platform

	// ParseLink 识别集面板贴的链接并返回规范化的 ref：同一个弹幕源不论链接怎么写，ref 都相同。
	// 不是本平台的链接返回 ErrUnrecognized；是本平台的但不能绑定（例如番剧一季的链接）返回 Kind 为 InvalidLink 的 *Error。
	// 可能联网，例如跟随短链跳转。
	ParseLink(ctx context.Context, link string) (Ref, error)
	// Describe 由 ref 生成展示用的弹幕源链接和标签，纯计算，不联网。
	Describe(ref Ref) (Display, error)
	// Fetch 按 ref 重新解析出当前的弹幕源（例如 B 站每次重新取 cid），取标题、时长和全部弹幕。
	// 全有或全无：任何一个请求最终失败，整次返回 *Error，不返回部分弹幕。
	// 重试已在内部做完；总时限由调用方的 ctx 决定，超时也返回 Kind 为 Upstream 的 *Error。
	Fetch(ctx context.Context, ref Ref) (Fetched, error)

	// ParseCollectionLink 识别季面板贴的链接，返回一个或多个候选合集，按适配器认为合适的顺序排列。
	// 同一个合集不论链接怎么写，候选的 ref 都相同。不是本平台的链接返回 ErrUnrecognized；
	// 是本平台的但不能作为合集绑定的，返回 Kind 为 InvalidLink 的 *Error，提示由适配器写。可能联网。
	ParseCollectionLink(ctx context.Context, link string) ([]CollectionCandidate, error)
	// ListCollection 按合集 ref 列出合集的标题、是否完结（取不到时为否）和全部条目，条目按合集里的顺序排列。
	// 合集不存在时返回 Kind 为 NotFound 的 *Error。重复序号不在这里判定，见 normalizeItems。
	ListCollection(ctx context.Context, ref CollectionRef) (Collection, error)
	// DescribeCollection 由合集 ref 生成展示用的链接和标签，纯计算，不联网。
	DescribeCollection(ref CollectionRef) (Display, error)
}

// FetchTimeout 调用方给一次拉取的总时限，创建绑定时也包括解析链接（跟随短链也要联网），季绑定的预览、创建与补建也用它限定
// 识别链接、列出合集。管理界面这些请求的超时（slowRequestTimeout，35 秒）比它多留出写库和响应的时间，改它时一起改那边。
// 超时由适配器按 Upstream 返回。
const FetchTimeout = 25 * time.Second

// Display 弹幕源在管理界面上的展示：链接和标签。
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

// String 日志里的名称。
func (k Kind) String() string {
	switch k {
	case InvalidLink:
		return "invalid_link"
	case NotFound:
		return "not_found"
	case AuthRequired:
		return "auth_required"
	case RateLimited:
		return "rate_limited"
	case Upstream:
		return "upstream"
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

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

// ErrUnrecognized 适配器不认识这个链接（ParseLink、ParseCollectionLink），Registry 会接着交给下一个适配器。
var ErrUnrecognized = errors.New("source: unrecognized link")

// Registry 已注册的源适配器。
type Registry struct {
	adapters []Adapter
}

// NewRegistry 按给定顺序注册，ParseLink 也按这个顺序询问。
func NewRegistry(adapters ...Adapter) *Registry {
	return &Registry{adapters: adapters}
}

// Get 按 bindings.adapter 取适配器；没有注册这个 ID 时返回错误，调用方按服务器内部错误处理。
func (r *Registry) Get(id string) (Adapter, error) {
	i := slices.IndexFunc(r.adapters, func(a Adapter) bool { return a.ID() == id })
	if i < 0 {
		return nil, fmt.Errorf("source: unknown adapter %q", id)
	}
	return r.adapters[i], nil
}

// ParseLink 依次交给各个适配器，返回第一个认识这个链接的适配器和规范化的 ref。
// 适配器返回 ErrUnrecognized 以外的错误时直接返回；都不认识时返回 Kind 为 InvalidLink 的 *Error（"无法识别的链接"）。
func (r *Registry) ParseLink(ctx context.Context, link string) (Adapter, Ref, error) {
	return firstRecognized(r.adapters, func(a Adapter) (Ref, error) { return a.ParseLink(ctx, link) })
}

// firstRecognized 依次用 parse 询问各个适配器，返回第一个认识链接的适配器和它的结果，规则见 Registry.ParseLink。
func firstRecognized[T any](adapters []Adapter, parse func(Adapter) (T, error)) (Adapter, T, error) {
	var zero T
	for _, a := range adapters {
		v, err := parse(a)
		switch {
		case errors.Is(err, ErrUnrecognized):
			continue
		case err != nil:
			return nil, zero, err
		}
		return a, v, nil
	}
	return nil, zero, &Error{Kind: InvalidLink, Message: "无法识别的链接"}
}
