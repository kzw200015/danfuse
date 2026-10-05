package bilibili

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/source"
)

// viewData x/web-interface/view 的 data，只取用到的字段。
type viewData struct {
	Title       string     `json:"title"`
	Pages       []viewPage `json:"pages"`
	RedirectURL string     `json:"redirect_url,omitempty"` // 只有番剧的稿件才有，指向 bangumi/play/ep…
	UGCSeason   *ugcSeason `json:"ugc_season,omitempty"`   // 只有属于投稿合集的稿件才有
}

// ugcSeason 稿件所在的投稿合集，含全部小节和条目，条目很多时也一次给全。
type ugcSeason struct {
	ID       int64        `json:"id"`
	Title    string       `json:"title"`
	Mid      int64        `json:"mid"` // 合集作者
	Sections []ugcSection `json:"sections"`
}

// ugcSection 合集的一个小节，由 UP 主自定义。
type ugcSection struct {
	Episodes []ugcEpisode `json:"episodes"`
}

// ugcEpisode 合集里的一个稿件。它的 cid 只是 P1 的，pages 才是这个稿件的全部分 P。
type ugcEpisode struct {
	Aid   int64     `json:"aid"`
	Title string    `json:"title"`
	Pages []ugcPage `json:"pages"`
}

type ugcPage struct {
	Page int `json:"page"`
}

// viewPage 一个分 P。时长取这里，不取 data.duration：那是所有分 P 的总时长。
type viewPage struct {
	Page     int    `json:"page"` // 从 1 开始
	CID      int64  `json:"cid"`
	Part     string `json:"part"`     // 分 P 标题
	Duration int    `json:"duration"` // 秒
}

// videoMeta 取投稿第 page 个分 P 的 cid、标题和时长。分 P 超出范围为 NotFound。
// 标题为"视频标题 / 分 P 标题"；只有一个分 P 时，分 P 标题多与视频标题重复或是上传的文件名，只用视频标题。
//
// 带 redirect_url 的稿件其实是番剧的单集，按番剧处理：再取它所在的季，标题为"番剧名 集标题"。
// 一个稿件可能有多个分 P、分别是不同的单集（例如先导 PV 和正式 PV），redirect_url 只指向第一个，
// 所以在这一季的单集里按这个分 P 的 cid 找对应的那一集；找不到时仍按普通投稿处理。
func (a *Adapter) videoMeta(ctx context.Context, aid int64, page int) (meta, error) {
	data, err := a.view(ctx, aid)
	if err != nil {
		return meta{}, err
	}
	i := slices.IndexFunc(data.Pages, func(p viewPage) bool { return p.Page == page })
	if i < 0 {
		return meta{}, sourceError(source.NotFound, fmt.Errorf("av%d 没有 P%d，共 %d 个分 P", aid, page, len(data.Pages)))
	}
	p := data.Pages[i]
	if p.CID <= 0 {
		return meta{}, sourceError(source.Upstream, fmt.Errorf("av%d 的 P%d 没有 cid", aid, page))
	}

	if ep, ok := redirectEpisode(data.RedirectURL); ok {
		s, err := a.season(ctx, ep)
		if err != nil {
			return meta{}, err
		}
		all := s.allEpisodes()
		if i := slices.IndexFunc(all, func(e pgcEpisode) bool { return e.CID == p.CID }); i >= 0 {
			return s.meta(all[i])
		}
	}

	title := strings.TrimSpace(data.Title)
	if part := strings.TrimSpace(p.Part); len(data.Pages) > 1 && part != "" {
		title += " / " + part
	}
	return meta{cid: p.CID, title: title, duration: p.Duration}, nil
}

// view 取投稿的元数据。NotFound 为"视频不存在、已删除或不可见"。
func (a *Adapter) view(ctx context.Context, aid int64) (viewData, error) {
	var data viewData
	err := a.client.getJSON(ctx, fmt.Sprintf("%s/x/web-interface/view?aid=%d", apiURL, aid), &data)
	return data, err
}

// redirectEpisode 从 view 的 redirect_url 取出番剧的 ep_id；没有 redirect_url 或不是番剧单集的链接时 ok 为 false。
func redirectEpisode(redirectURL string) (int64, bool) {
	u, err := url.Parse(redirectURL)
	if err != nil {
		return 0, false
	}
	t := parseURL(u)
	return t.id, t.kind == targetEpisode
}
