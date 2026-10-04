package bilibili

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/source"
)

// meta 解析出的弹幕源：拉弹幕用的 cid，以及绑定的标题和时长。
type meta struct {
	cid      int64
	title    string
	duration int // 秒
}

// viewData x/web-interface/view 的 data，只取用到的字段。
type viewData struct {
	Title       string     `json:"title"`
	Pages       []viewPage `json:"pages"`
	RedirectURL string     `json:"redirect_url,omitempty"` // 只有番剧的稿件才有，指向 bangumi/play/ep…
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
// 带 redirect_url 的稿件其实是番剧的单集，按番剧处理：再取它所在的整季，标题为"番剧名 集标题"。
// 一个稿件可能有多个分 P、分别是不同的单集（例如先导 PV 和正式 PV），redirect_url 只指向第一个，
// 所以在整季里按这个分 P 的 cid 找对应的那一集；找不到时仍按普通投稿处理。
func (a *Adapter) videoMeta(ctx context.Context, aid int64, page int) (meta, error) {
	target := fmt.Sprintf("%s/x/web-interface/view?aid=%d", apiURL, aid)
	var data viewData
	if err := a.client.getJSON(ctx, target, &data); err != nil {
		return meta{}, err
	}
	i := slices.IndexFunc(data.Pages, func(p viewPage) bool { return p.Page == page })
	if i < 0 {
		return meta{}, sourceError(source.NotFound, fmt.Errorf("av%d 没有 P%d，共 %d 个分 P", aid, page, len(data.Pages)))
	}
	p := data.Pages[i]
	if p.CID <= 0 {
		return meta{}, sourceError(source.Upstream, fmt.Errorf("GET %s: P%d 没有 cid", target, page))
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

// redirectEpisode 从 view 的 redirect_url 取出番剧的 ep_id；没有 redirect_url 或不是番剧单集的链接时 ok 为 false。
func redirectEpisode(redirectURL string) (int64, bool) {
	u, err := url.Parse(redirectURL)
	if err != nil {
		return 0, false
	}
	v, err := parseURL(u)
	return v.EpID, err == nil && v.Kind == kindEpisode
}
