// Package bilibili B 站源适配器。B 站只有这一个适配器，投稿和番剧用 ref 的 kind 区分：
// 拉弹幕都按 cid 请求同样的接口，弹幕 ID 在同一个空间里；令牌桶、SESSDATA 与重试策略共用；链接解析会跨类型。
//
// 文件划分：
//   - bilibili.go：source.Adapter 里弹幕源的方法，ref 的结构，Fetch 的编排（元数据 → protobuf 分段与 XML → 按 ID 合并），
//     以及共用的元数据 meta 和弹幕字段的映射 newDanmaku；
//   - collection.go：source.Adapter 里合集的方法：合集 ref 的结构，季面板链接的识别，番剧的一季、投稿合集、多 P 投稿的列出；
//   - link.go：链接解析（集面板与季面板共用，各自决定接受哪些）、短链跳转，BV 号与 aid 互转；
//   - client.go：HTTP 层：UA、Referer 与 SESSDATA、全局令牌桶、重试与退避、错误归类与给用户的提示；
//   - view.go：投稿的元数据，解析出 cid、标题、时长；带 redirect_url 的转给番剧；
//   - pgc.go：番剧单集的元数据，番剧一季的结构；
//   - seg.go：protobuf 分段弹幕的拉取与 protowire 解码；
//   - xml.go：XML 弹幕的拉取、解析，与 protobuf 的合并。
//
// 测试平时只回放 testdata/ 里脱敏后的样本，不联网；请求真实 B 站的 live 模式见 live_test.go。
package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/kzw200015/danfuse/backend/internal/config"
	"github.com/kzw200015/danfuse/backend/internal/danmaku"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// ref bindings.ref 的结构，kind 区分弹幕源的种类：
//   - 投稿 {"kind":"video","aid":N,"page":P}：aid 是规范 ID，BV 号只在展示时由 aid 算出；
//   - 番剧单集 {"kind":"episode","epId":N}。
//
// cid 不进 ref，每次拉取都重新解析。
type ref struct {
	Kind string `json:"kind"`
	Aid  int64  `json:"aid,omitempty"`
	Page int    `json:"page,omitempty"` // 从 1 开始
	EpID int64  `json:"epId,omitempty"`
}

const (
	kindVideo   = "video"
	kindEpisode = "episode"
)

// decodeRef 解析并校验 bindings.ref。
func decodeRef(r source.Ref) (ref, error) {
	var v ref
	if err := json.Unmarshal(r, &v); err != nil {
		return ref{}, fmt.Errorf("bilibili: decode ref %s: %w", r, err)
	}
	switch {
	case v.Kind == kindVideo && v.Aid > 0 && v.Aid < maxAid && v.Page >= 1 && v.EpID == 0,
		v.Kind == kindEpisode && v.EpID > 0 && v.Aid == 0 && v.Page == 0:
		return v, nil
	}
	return ref{}, fmt.Errorf("bilibili: invalid ref %s", r)
}

// encode 编码成 bindings.ref。字段只有字符串和整数，json.Marshal 不会出错。
func (v ref) encode() source.Ref {
	b, _ := json.Marshal(v)
	return b
}

type Adapter struct {
	client *client
}

var _ source.Adapter = (*Adapter)(nil)

// New 不连 B 站。同一个进程里只应有一个 Adapter：令牌桶在它里面，所有绑定共用。
func New(cfg config.Bilibili) *Adapter {
	return &Adapter{client: newClient(cfg.Sessdata)}
}

// ID 存入 bindings.adapter，一经发布不能再改。
func (a *Adapter) ID() string { return "bilibili" }

func (a *Adapter) Platform() danmaku.Platform { return danmaku.PlatformBilibili }

// Describe 投稿显示由 aid 算出的 BV 号，分 P 不是第 1 个时加上 P 几；番剧显示 ep 号。
func (a *Adapter) Describe(r source.Ref) (source.Display, error) {
	v, err := decodeRef(r)
	if err != nil {
		return source.Display{}, err
	}
	if v.Kind == kindEpisode {
		ep := "ep" + strconv.FormatInt(v.EpID, 10)
		return source.Display{URL: "https://www.bilibili.com/bangumi/play/" + ep, Label: "B 站番剧 " + ep}, nil
	}
	bvid := aidToBV(v.Aid)
	d := source.Display{URL: "https://www.bilibili.com/video/" + bvid, Label: "B 站投稿 " + bvid}
	if v.Page > 1 {
		d.URL += "?p=" + strconv.Itoa(v.Page)
		d.Label += " P" + strconv.Itoa(v.Page)
	}
	return d, nil
}

// ParseLink 接受投稿、番剧单集的链接和裸 ID（见 parseURL），以及 b23.tv、bili2233.cn 的短链。
// 只有短链要联网：请求一次，按跳转到的长链接解析。同一个弹幕源不论链接怎么写，ref 都相同；
// 投稿链接不联网，所以番剧的稿件（av、BV）与它的 ep 是不同的 ref，拉取时才按番剧处理。
// 番剧一季的 ss 链接、作品页与空间里的合集页返回 InvalidLink，提示到季面板绑定；系列页与其他链接一样认不出。
func (a *Adapter) ParseLink(ctx context.Context, link string) (source.Ref, error) {
	t, err := a.resolve(ctx, link)
	if err != nil {
		return nil, err
	}
	switch t.kind {
	case targetVideo:
		return ref{Kind: kindVideo, Aid: t.id, Page: t.page}.encode(), nil
	case targetEpisode:
		return ref{Kind: kindEpisode, EpID: t.id}.encode(), nil
	case targetSeason, targetMedia, targetUGCSeason:
		return nil, &source.Error{Kind: source.InvalidLink, Message: "整季或合集的链接请在季面板绑定", Err: fmt.Errorf("%s %d 是合集", t.kind, t.id)}
	}
	return nil, t.unsupported("短链指向的不是投稿或番剧单集")
}

// meta 解析出的弹幕源：拉弹幕用的 cid，以及绑定的标题和时长。
type meta struct {
	cid      int64
	title    string
	duration int // 秒
}

// Fetch 重新取元数据（cid、标题、时长），再拉取全部 protobuf 分段和 XML，按原始 ID 合并。全有或全无。
func (a *Adapter) Fetch(ctx context.Context, r source.Ref) (source.Fetched, error) {
	v, err := decodeRef(r)
	if err != nil {
		return source.Fetched{}, err
	}
	var m meta
	if v.Kind == kindEpisode {
		m, err = a.episodeMeta(ctx, v.EpID)
	} else {
		m, err = a.videoMeta(ctx, v.Aid, v.Page)
	}
	if err != nil {
		return source.Fetched{}, err
	}
	proto, xml, err := a.fetchDanmaku(ctx, m.cid, m.duration)
	if err != nil {
		return source.Fetched{}, err
	}
	items, overlap := mergeXML(proto, xml)
	return source.Fetched{
		Title:    m.title,
		Duration: m.duration,
		Danmaku:  items,
		LogAttrs: []slog.Attr{
			slog.Int64("cid", m.cid),
			slog.Int("protobuf", len(proto)),
			slog.Int("xml", len(xml)),
			slog.Int("overlap", overlap),
		},
	}, nil
}

// fetchDanmaku 并发拉取 cid 的 XML 和全部 protobuf 分段：段数为 ceil(时长 / 360)（至少 1 段），按段的顺序拼接。
// 同时进行的请求不超过 segmentConcurrency 个；任何一个最终失败都整体失败，不返回部分弹幕。
// 配置了 SESSDATA 也照样拉 XML：登录后的 protobuf 是否已经覆盖了它，还没有验证。
func (a *Adapter) fetchDanmaku(ctx context.Context, cid int64, duration int) (proto, xml []danmaku.Danmaku, err error) {
	parts := make([][]danmaku.Danmaku, max(1, (duration+segmentSeconds-1)/segmentSeconds))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(segmentConcurrency)
	g.Go(func() error {
		var err error
		xml, err = a.xmlDanmaku(ctx, cid)
		return err
	})
	for i := range parts {
		g.Go(func() error {
			var err error
			parts[i], err = a.segment(ctx, cid, i+1)
			return err
		})
	}
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}
	return slices.Concat(parts...), xml, nil
}

// newDanmaku 把 B 站一条弹幕的字段映射成内部格式，protobuf 与 XML 共用；不是普通的文字弹幕、没有 ID 或正文为空时 ok 为 false。
//   - 模式：1、2、3 合并为滚动，4、5、6 原样保留，7、8、9（高级、代码、BAS）和其他取值丢弃；
//   - 颜色：只取低 24 位，大会员渐变色忽略；
//   - 正文：只清洗非法的 UTF-8 字节和 NUL（PostgreSQL 的 text 存不了），其余原样保留；去掉空白后为空的丢弃。
func newDanmaku(id int64, timeMs int32, mode int64, color uint64, text string) (danmaku.Danmaku, bool) {
	d := danmaku.Danmaku{SourceID: id, TimeMs: timeMs, Color: uint32(color & 0xFFFFFF)}
	switch mode {
	case 1, 2, 3:
		d.Mode = danmaku.ModeScroll
	case 4, 5, 6:
		d.Mode = danmaku.Mode(mode)
	default:
		return danmaku.Danmaku{}, false
	}
	d.Text = strings.ReplaceAll(strings.ToValidUTF8(text, ""), "\x00", "")
	if d.SourceID == 0 || strings.TrimSpace(d.Text) == "" {
		return danmaku.Danmaku{}, false
	}
	return d, true
}
