// Package bilibili B 站源适配器。B 站只有这一个适配器，不同种类的弹幕源用 ref 的 kind 区分：
// 拉弹幕都按 cid 请求同样的接口，弹幕 ID 在同一个空间里；令牌桶与重试策略共用。
//
// 文件划分：
//   - bilibili.go：Adapter / Linker 的实现，ref 的结构，Fetch 的编排（元数据 → 分段弹幕）；
//   - link.go：链接解析，BV 号与 aid 互转；
//   - client.go：HTTP 层：UA 与 Referer、全局令牌桶、重试与退避、错误归类；
//   - view.go：投稿的元数据，解析出 cid、标题、时长；
//   - seg.go：分段弹幕的并发拉取、protowire 解码与字段映射。
//
// 测试平时只回放 testdata/ 里脱敏后的样本，不联网；请求真实 B 站的 live 模式见 live_test.go。
package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// ref bindings.ref 的结构，kind 区分弹幕源的种类。投稿为 {"kind":"video","aid":N,"page":P}：
// aid 是规范 ID，BV 号只在展示时由 aid 算出；cid 不进 ref，每次拉取都重新解析。
type ref struct {
	Kind string `json:"kind"`
	Aid  int64  `json:"aid,omitempty"`
	Page int    `json:"page,omitempty"` // 从 1 开始
}

const kindVideo = "video"

// decodeRef 解析并校验 bindings.ref。
func decodeRef(r source.Ref) (ref, error) {
	var v ref
	if err := json.Unmarshal(r, &v); err != nil {
		return ref{}, fmt.Errorf("bilibili: decode ref %s: %w", r, err)
	}
	if v.Kind != kindVideo || v.Aid <= 0 || v.Aid >= maxAid || v.Page < 1 {
		return ref{}, fmt.Errorf("bilibili: invalid ref %s", r)
	}
	return v, nil
}

type Adapter struct {
	client *client
}

var (
	_ source.Adapter = (*Adapter)(nil)
	_ source.Linker  = (*Adapter)(nil)
)

// New 不连 B 站。同一个进程里只应有一个 Adapter：令牌桶在它里面，所有绑定共用。
func New() *Adapter {
	return &Adapter{client: newClient()}
}

// ID 存入 bindings.adapter，一经发布不能再改。
func (a *Adapter) ID() string { return "bilibili" }

func (a *Adapter) Platform() danmaku.Platform { return danmaku.PlatformBilibili }

// Describe 投稿显示由 aid 算出的 BV 号，分 P 不是第 1 个时加上 P 几。
func (a *Adapter) Describe(r source.Ref) (source.Display, error) {
	v, err := decodeRef(r)
	if err != nil {
		return source.Display{}, err
	}
	bvid := aidToBV(v.Aid)
	d := source.Display{URL: "https://www.bilibili.com/video/" + bvid, Label: "B 站投稿 " + bvid}
	if v.Page > 1 {
		d.URL += "?p=" + strconv.Itoa(v.Page)
		d.Label += " P" + strconv.Itoa(v.Page)
	}
	return d, nil
}

// ParseLink 接受投稿链接 video/BV…、video/av…（可带 ?p=，缺省为 1）和裸的 BV、av 号，不联网。
// 同一个分 P 不论贴 BV 还是 av，ref 都相同。
func (a *Adapter) ParseLink(_ context.Context, link string) (source.Ref, error) {
	v, ok := parseLink(link)
	if !ok {
		return nil, source.ErrUnrecognized
	}
	return json.Marshal(v)
}

// Fetch 重新取元数据（cid、标题、时长），再按 ceil(时长 / 360) 段并发拉取 protobuf 分段弹幕。全有或全无。
func (a *Adapter) Fetch(ctx context.Context, r source.Ref) (source.Fetched, error) {
	v, err := decodeRef(r)
	if err != nil {
		return source.Fetched{}, err
	}
	m, err := a.videoMeta(ctx, v.Aid, v.Page)
	if err != nil {
		return source.Fetched{}, err
	}
	items, err := a.segments(ctx, m.cid, m.duration)
	if err != nil {
		return source.Fetched{}, err
	}
	return source.Fetched{
		Title:    m.title,
		Duration: m.duration,
		Danmaku:  items,
		LogAttrs: []slog.Attr{slog.Int64("cid", m.cid), slog.Int("protobuf", len(items))},
	}, nil
}
