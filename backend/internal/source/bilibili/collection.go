package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/source"
)

// collectionRef season_bindings.ref 的结构，kind 区分合集的种类，也是候选的种类标识：
//   - 番剧的一季 {"kind":"bangumi","seasonId":N}；
//   - 投稿合集 {"kind":"ugcSeason","seasonId":N,"mid":M}：mid 是合集的作者，只用来生成展示的链接（空间里的合集页要带 mid）。
//     它总是取自 B 站的接口（合集条目列表的 meta.mid、稿件 view 里 ugc_season.mid，两者都是合集作者），不取用户贴的链接里的，
//     所以同一个合集的 ref 总是相同；查重和唯一约束依赖这一点；
//   - 多 P 投稿 {"kind":"multiPage","aid":N}。
type collectionRef struct {
	Kind     string `json:"kind"`
	SeasonID int64  `json:"seasonId,omitempty"`
	Mid      int64  `json:"mid,omitempty"`
	Aid      int64  `json:"aid,omitempty"`
}

const (
	collectionBangumi   = "bangumi"
	collectionUGCSeason = "ugcSeason"
	collectionMultiPage = "multiPage"
)

// archivesPageSize 合集条目列表每页的条数，B 站允许的最大值。
const archivesPageSize = 100

// maxArchivePages 列合集条目最多翻的页数，防止接口异常时一直翻下去。
const maxArchivePages = 200

var _ source.Collector = (*Adapter)(nil)

// decodeCollectionRef 解析并校验 season_bindings.ref。
func decodeCollectionRef(r source.CollectionRef) (collectionRef, error) {
	var v collectionRef
	if err := json.Unmarshal(r, &v); err != nil {
		return collectionRef{}, fmt.Errorf("bilibili: decode collection ref %s: %w", r, err)
	}
	switch {
	case v.Kind == collectionBangumi && v.SeasonID > 0 && v.Mid == 0 && v.Aid == 0,
		v.Kind == collectionUGCSeason && v.SeasonID > 0 && v.Mid > 0 && v.Aid == 0,
		v.Kind == collectionMultiPage && v.Aid > 0 && v.Aid < maxAid && v.SeasonID == 0 && v.Mid == 0:
		return v, nil
	}
	return collectionRef{}, fmt.Errorf("bilibili: invalid collection ref %s", r)
}

// encode 编码成 season_bindings.ref。字段只有字符串和整数，json.Marshal 不会出错。
func (v collectionRef) encode() source.CollectionRef {
	b, _ := json.Marshal(v)
	return b
}

// candidate 由合集 ref 构造候选，种类标识就是 ref 的 kind。
func candidate(v collectionRef) source.CollectionCandidate {
	return source.CollectionCandidate{Kind: v.Kind, Ref: v.encode()}
}

// ParseCollectionLink 识别季面板贴的链接：
//   - 番剧：一季的 ss 链接不联网；作品页 md 换算成 season_id（换算结果为 0 时为 NotFound）；单集 ep、番剧的稿件取它所在的季；
//   - 空间里的合集页：请求一次合集的条目列表，取作者的 mid；
//   - 普通稿件：有多个分 P 时候选里有多 P 投稿，属于合集时候选里有投稿合集，按这个顺序；两者都不是时为 InvalidLink；
//   - 系列、没写 type 的列表页为 InvalidLink；
//   - 指向以上页面的短链。
func (a *Adapter) ParseCollectionLink(ctx context.Context, link string) ([]source.CollectionCandidate, error) {
	t, err := a.resolve(ctx, link)
	if err != nil {
		return nil, err
	}
	switch t.kind {
	case targetSeason:
		return bangumiCandidates(t.id), nil
	case targetMedia:
		seasonID, err := a.mediaSeason(ctx, t.id)
		if err != nil {
			return nil, err
		}
		return bangumiCandidates(seasonID), nil
	case targetEpisode:
		s, err := a.season(ctx, t.id)
		if err != nil {
			return nil, err
		}
		return bangumiCandidates(s.SeasonID), nil
	case targetUGCSeason:
		return a.ugcSeasonCandidates(ctx, t.id)
	case targetVideo:
		return a.videoCandidates(ctx, t.id)
	case targetSeries:
		return nil, &source.Error{Kind: source.InvalidLink, Message: "暂不支持系列", Err: fmt.Errorf("%s 是系列", link)}
	case targetLists:
		return nil, &source.Error{
			Kind: source.InvalidLink, Message: "链接里没有写明是合集还是系列，请从合集页重新复制链接",
			Err: fmt.Errorf("%s 没有 type", link),
		}
	}
	return nil, t.unsupported("短链指向的不是番剧、合集或投稿")
}

// bangumiCandidates 番剧的一季这一个候选。
func bangumiCandidates(seasonID int64) []source.CollectionCandidate {
	return []source.CollectionCandidate{candidate(collectionRef{Kind: collectionBangumi, SeasonID: seasonID})}
}

// ugcSeasonCandidates 空间里的合集页这一个候选：请求一次合集的条目列表，取作者的 mid。
func (a *Adapter) ugcSeasonCandidates(ctx context.Context, seasonID int64) ([]source.CollectionCandidate, error) {
	first, err := a.archives(ctx, seasonID, 1)
	if err != nil {
		return nil, err
	}
	if first.Meta.Mid <= 0 {
		return nil, sourceError(source.Upstream, fmt.Errorf("合集 %d 的作者 mid 为 %d", seasonID, first.Meta.Mid))
	}
	return []source.CollectionCandidate{candidate(collectionRef{Kind: collectionUGCSeason, SeasonID: seasonID, Mid: first.Meta.Mid})}, nil
}

// mediaSeason 作品页 md 换算成 season_id。md 不存在时接口照样成功，只是 season_id 为 0，按 NotFound 处理。
func (a *Adapter) mediaSeason(ctx context.Context, mediaID int64) (int64, error) {
	var data struct {
		Media struct {
			SeasonID int64 `json:"season_id"`
		} `json:"media"`
	}
	err := a.client.getJSON(ctx, fmt.Sprintf("%s/pgc/review/user?media_id=%d", apiURL, mediaID), &data)
	if err == nil && data.Media.SeasonID <= 0 {
		err = sourceError(source.NotFound, fmt.Errorf("md%d 的 season_id 为 0", mediaID))
	}
	return data.Media.SeasonID, notFoundAs(err, bangumiNotFound)
}

// videoCandidates 普通稿件链接的候选。番剧的稿件（带 redirect_url）只有它所在的番剧的一季。
func (a *Adapter) videoCandidates(ctx context.Context, aid int64) ([]source.CollectionCandidate, error) {
	v, err := a.view(ctx, aid)
	if err != nil {
		return nil, err
	}
	if ep, ok := redirectEpisode(v.RedirectURL); ok {
		s, err := a.season(ctx, ep)
		if err != nil {
			return nil, err
		}
		return bangumiCandidates(s.SeasonID), nil
	}
	var candidates []source.CollectionCandidate
	if len(v.Pages) > 1 {
		candidates = append(candidates, candidate(collectionRef{Kind: collectionMultiPage, Aid: aid}))
	}
	if s := v.UGCSeason; s != nil {
		if s.ID <= 0 || s.Mid <= 0 {
			return nil, sourceError(source.Upstream, fmt.Errorf("av%d 所在的合集 id 为 %d、作者 mid 为 %d", aid, s.ID, s.Mid))
		}
		candidates = append(candidates, candidate(collectionRef{Kind: collectionUGCSeason, SeasonID: s.ID, Mid: s.Mid}))
	}
	if len(candidates) == 0 {
		return nil, &source.Error{
			Kind: source.InvalidLink, Message: "这个稿件只有一个分 P，也不属于合集，请在集面板绑定",
			Err: fmt.Errorf("av%d 只有 %d 个分 P，不属于合集", aid, len(v.Pages)),
		}
	}
	return candidates, nil
}

// ListCollection 列出合集：
//   - 番剧：一次取出这一季的全部单集，只取正片（section_type 为 0），混在里面的预告与 section 里的 PV、OP 等都不是条目；
//     集号是整数时为序号，否则对不上；标签用 show_title；完结标志取 publish.is_finish；
//   - 投稿合集：按条目列表的顺序（各小节按 UP 主排的顺序连起来）取位置为序号，标签用稿件标题；
//     再取第一个稿件的 view，从合集信息里得到每个稿件的分 P 数，多于一个时提示只用 P1；没有完结标志；
//   - 多 P 投稿：分 P 号为序号，标签为"P{n} 分 P 标题"；没有完结标志。
func (a *Adapter) ListCollection(ctx context.Context, r source.CollectionRef) (source.Collection, error) {
	v, err := decodeCollectionRef(r)
	if err != nil {
		return source.Collection{}, err
	}
	switch v.Kind {
	case collectionBangumi:
		return a.listBangumi(ctx, v.SeasonID)
	case collectionUGCSeason:
		return a.listUGCSeason(ctx, v.SeasonID)
	default:
		return a.listPages(ctx, v.Aid)
	}
}

func (a *Adapter) listBangumi(ctx context.Context, seasonID int64) (source.Collection, error) {
	var s seasonData
	err := a.client.getJSON(ctx, fmt.Sprintf("%s/pgc/view/web/season?season_id=%d", apiURL, seasonID), &s)
	if err != nil {
		return source.Collection{}, notFoundAs(err, bangumiNotFound)
	}
	c := source.Collection{Title: strings.TrimSpace(s.Title), Finished: s.Publish.IsFinish == 1}
	for _, e := range s.Episodes {
		if e.SectionType != 0 || e.ID <= 0 {
			continue
		}
		item := source.CollectionItem{Ref: ref{Kind: kindEpisode, EpID: e.ID}.encode(), Label: e.label()}
		if n, ok := episodeNumber(e.Title); ok {
			item.Number = n
		} else {
			item.Unmatched = fmt.Sprintf("集号「%s」不是整数", strings.TrimSpace(e.Title))
		}
		c.Items = append(c.Items, item)
	}
	return c, nil
}

// episodeNumber 番剧的集号是整数时的值；"SP""OAD02""24.9" 这类不是。
func episodeNumber(title string) (int, bool) {
	s := strings.TrimSpace(title)
	if s == "" || strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' }) {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 32)
	return int(n), err == nil && n <= math.MaxInt32
}

func (a *Adapter) listUGCSeason(ctx context.Context, seasonID int64) (source.Collection, error) {
	first, err := a.archives(ctx, seasonID, 1)
	if err != nil {
		return source.Collection{}, err
	}
	entries := first.Archives
	for page := 2; len(entries) < first.Page.Total && page <= maxArchivePages; page++ {
		next, err := a.archives(ctx, seasonID, page)
		if err != nil {
			return source.Collection{}, err
		}
		if len(next.Archives) == 0 {
			break
		}
		entries = append(entries, next.Archives...)
	}

	// 条目列表里没有分 P，合集里任何一个稿件的 view 都带着整个合集各稿件的分 P
	pages := make(map[int64]int)
	if len(entries) > 0 {
		v, err := a.view(ctx, entries[0].Aid)
		srcErr, _ := errors.AsType[*source.Error](err)
		switch {
		case srcErr != nil && srcErr.Kind == source.NotFound: // 第一个稿件刚被删除：不提示分 P 数
		case err != nil:
			return source.Collection{}, err
		case v.UGCSeason != nil && v.UGCSeason.ID == seasonID:
			for _, sec := range v.UGCSeason.Sections {
				for _, e := range sec.Episodes {
					pages[e.Aid] = len(e.Pages)
				}
			}
		}
	}

	c := source.Collection{Title: strings.TrimSpace(first.Meta.Title)}
	for i, e := range entries {
		if e.Aid <= 0 || e.Aid >= maxAid {
			continue
		}
		item := source.CollectionItem{Ref: ref{Kind: kindVideo, Aid: e.Aid, Page: 1}.encode(), Number: i + 1, Label: strings.TrimSpace(e.Title)}
		if n := pages[e.Aid]; n > 1 {
			item.Note = fmt.Sprintf("共 %d 个分 P，只用 P1", n)
		}
		c.Items = append(c.Items, item)
	}
	return c, nil
}

// archivesData x/polymer/web-space/seasons_archives_list 的 data：合集的一页条目，顺序与 view 里各小节展开后的顺序一致。
type archivesData struct {
	Archives []archive     `json:"archives"`
	Meta     archivesMeta  `json:"meta"`
	Page     archivesPager `json:"page"`
}

type archivesMeta struct {
	Mid   int64  `json:"mid"`   // 合集作者
	Title string `json:"title"` // 合集标题，不带"合集·"前缀
}

type archivesPager struct {
	Total int `json:"total"` // 条目总数
}

type archive struct {
	Aid   int64  `json:"aid"`
	Title string `json:"title"`
}

// archives 取投稿合集第 page 页（从 1 开始）的条目。不需要作者的 mid，填 0 即可。合集不存在时为 NotFound。
func (a *Adapter) archives(ctx context.Context, seasonID int64, page int) (archivesData, error) {
	var data archivesData
	target := fmt.Sprintf("%s/x/polymer/web-space/seasons_archives_list?mid=0&season_id=%d&sort_reverse=false&page_num=%d&page_size=%d",
		apiURL, seasonID, page, archivesPageSize)
	err := a.client.getJSON(ctx, target, &data)
	return data, notFoundAs(err, ugcSeasonNotFound)
}

func (a *Adapter) listPages(ctx context.Context, aid int64) (source.Collection, error) {
	v, err := a.view(ctx, aid)
	if err != nil {
		return source.Collection{}, err
	}
	c := source.Collection{Title: strings.TrimSpace(v.Title)}
	for _, p := range v.Pages {
		if p.Page < 1 {
			continue
		}
		label := strings.TrimSpace(fmt.Sprintf("P%d %s", p.Page, strings.TrimSpace(p.Part)))
		c.Items = append(c.Items, source.CollectionItem{Ref: ref{Kind: kindVideo, Aid: aid, Page: p.Page}.encode(), Number: p.Page, Label: label})
	}
	return c, nil
}

// DescribeCollection 番剧显示 ss 号，投稿合集显示合集 ID、链接到作者空间里的合集页，多 P 投稿显示由 aid 算出的 BV 号。
func (a *Adapter) DescribeCollection(r source.CollectionRef) (source.Display, error) {
	v, err := decodeCollectionRef(r)
	if err != nil {
		return source.Display{}, err
	}
	switch v.Kind {
	case collectionBangumi:
		ss := "ss" + strconv.FormatInt(v.SeasonID, 10)
		return source.Display{URL: "https://www.bilibili.com/bangumi/play/" + ss, Label: "B 站番剧 " + ss}, nil
	case collectionUGCSeason:
		return source.Display{
			URL:   fmt.Sprintf("https://space.bilibili.com/%d/lists/%d?type=season", v.Mid, v.SeasonID),
			Label: "B 站投稿合集 " + strconv.FormatInt(v.SeasonID, 10),
		}, nil
	default:
		bvid := aidToBV(v.Aid)
		return source.Display{URL: "https://www.bilibili.com/video/" + bvid, Label: "B 站多 P 投稿 " + bvid}, nil
	}
}
