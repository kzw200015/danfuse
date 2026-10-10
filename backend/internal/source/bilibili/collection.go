package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// decodeCollectionRef 解析并校验 season_bindings.ref。
func decodeCollectionRef(r source.CollectionRef) (collectionRef, error) {
	var v collectionRef
	if err := json.Unmarshal(r, &v); err != nil {
		return collectionRef{}, fmt.Errorf("bilibili: decode collection ref %s: %w", r, err)
	}
	switch {
	case v.Kind == collectionBangumi && v.SeasonID > 0 && v.Mid == 0 && v.Aid == 0,
		v.Kind == collectionUGCSeason && v.SeasonID > 0 && v.Mid > 0 && v.Aid == 0,
		v.Kind == collectionMultiPage && validAid(v.Aid) && v.SeasonID == 0 && v.Mid == 0:
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
//   - 投稿合集：展开到分 P，按条目列表的顺序（各小节按 UP 主排的顺序连起来）、每个稿件按分 P 号排列；
//     分 P 取自合集里一个稿件的 view（它带着整个合集各稿件的分 P）；没有完结标志；
//   - 多 P 投稿：各个分 P；没有完结标志。
//
// 投稿合集和多 P 投稿的标签为"稿件标题 / 分 P 标题"（见 pageLabel），序号由季绑定的集号规则从标签认出（NumberedByRule）。
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
			item.Unmatched = source.NotInteger(strings.TrimSpace(e.Title))
		}
		c.Items = append(c.Items, item)
	}
	return c, nil
}

// episodeNumber 番剧的集号是整数时的值；"SP""OAD02""24.9" 这类不是。
func episodeNumber(title string) (int, bool) {
	n, err := strconv.ParseUint(strings.TrimSpace(title), 10, 31) // 不接受正负号，超出 int32 时报错
	return int(n), err == nil
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

	pages, err := a.seasonPages(ctx, seasonID, entries)
	if err != nil {
		return source.Collection{}, err
	}

	c := source.Collection{Title: strings.TrimSpace(first.Meta.Title), NumberedByRule: true}
	for _, e := range entries {
		if !validAid(e.Aid) {
			continue
		}
		ps, ok := pages[e.Aid]
		if !ok { // 刚加进合集、view 里还没有：只有 P1
			ps = []ugcPage{{Page: 1}}
		}
		for _, p := range ps {
			if p.Page >= 1 {
				c.Items = append(c.Items, source.CollectionItem{
					Ref: ref{Kind: kindVideo, Aid: e.Aid, Page: p.Page}.encode(), Label: pageLabel(e.Title, p.Part),
				})
			}
		}
	}
	return c, nil
}

// maxViewAttempts 列投稿合集时最多取几个稿件的 view 来找各稿件的分 P。
const maxViewAttempts = 3

// seasonPages 投稿合集各稿件的分 P，按 aid。条目列表里没有分 P，合集里任何一个稿件的 view 都带着整个合集各稿件的分 P：
// 按顺序取 view，稿件刚被删除（NotFound）、或 view 里已经不是这个合集时换下一个，最多取 maxViewAttempts 个；
// 都取不到时为上游错误。合集为空时不请求。
func (a *Adapter) seasonPages(ctx context.Context, seasonID int64, entries []archive) (map[int64][]ugcPage, error) {
	attempts := 0
	for _, e := range entries {
		if !validAid(e.Aid) {
			continue
		}
		if attempts == maxViewAttempts {
			break
		}
		attempts++
		v, err := a.view(ctx, e.Aid)
		srcErr, _ := errors.AsType[*source.Error](err)
		switch {
		case srcErr != nil && srcErr.Kind == source.NotFound:
			continue
		case err != nil:
			return nil, err
		case v.UGCSeason == nil || v.UGCSeason.ID != seasonID:
			continue
		}
		pages := make(map[int64][]ugcPage)
		for _, sec := range v.UGCSeason.Sections {
			for _, ep := range sec.Episodes {
				pages[ep.Aid] = ep.Pages
			}
		}
		return pages, nil
	}
	if attempts == 0 {
		return nil, nil
	}
	return nil, sourceError(source.Upstream, fmt.Errorf("合集 %d 的前 %d 个稿件都取不到带合集信息的 view", seasonID, attempts))
}

// pageLabel 投稿合集、多 P 投稿条目的标签"稿件标题 / 分 P 标题"；分 P 标题为空或与稿件标题相同时只有稿件标题。
func pageLabel(title, part string) string {
	title, part = strings.TrimSpace(title), strings.TrimSpace(part)
	if part == "" || part == title {
		return title
	}
	return title + source.LabelSeparator + part
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
	c := source.Collection{Title: strings.TrimSpace(v.Title), NumberedByRule: true}
	for _, p := range v.Pages {
		if p.Page >= 1 {
			c.Items = append(c.Items, source.CollectionItem{Ref: ref{Kind: kindVideo, Aid: aid, Page: p.Page}.encode(), Label: pageLabel(v.Title, p.Part)})
		}
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
