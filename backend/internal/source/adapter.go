// Package source 源适配器的接口：与某个平台通信，把用户贴的链接认成弹幕源或合集，拉取弹幕、列出合集。
// 适配器只负责请求、限速、重试、解析，不含匹配、缓存和存储决策，不访问数据库。
// 各平台的适配器在子包里（bilibili），由 app 注册进 Registry；业务代码只依赖这里的接口。
//   - adapter.go：适配器的接口与交换的类型（Ref、Fetched）；collection.go：合集的类型；
//   - error.go：适配器的错误；registry.go：按链接选出适配器的注册表；
//   - episode_rule.go：集号规则（从合集条目的标签认出集号）；episode_mapping.go：集号对应（条目的集号落到哪一集）。
package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// Ref 弹幕源在平台内的引用，是适配器自己定义的 JSON，存入 bindings.ref（jsonb），
// 参与唯一约束 (episode_id, adapter, ref)。适配器之外的代码不解析它。
type Ref []byte

// CanonicalRef 返回 ref 的规范写法，用来比较两个 ref 是不是同一个弹幕源：对象的键按顺序排列、没有多余的空格、
// 数字保留原文（大整数不丢精度）、字符串的转义统一。从 jsonb 读出的文本带空格、键会重排，不能直接逐字节比较。
// 季绑定的派生把适配器列出的、条目表里存的和绑定上存的 ref 都经它规范化后再比较。
// ref 不是单个 JSON 值时原样返回（数据库和适配器给出的 ref 都是合法的 JSON，不会走到这里）。
func CanonicalRef(ref Ref) string {
	dec := json.NewDecoder(bytes.NewReader(ref))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(ref)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return string(ref)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return string(ref)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

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
	// 合集不存在时返回 Kind 为 NotFound 的 *Error。重复的 ref 和序号不在这里处理，见 NumberItems。
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
