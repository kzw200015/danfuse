package bilibili

import (
	"context"
	"fmt"
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
	Title string     `json:"title"`
	Pages []viewPage `json:"pages"`
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
func (a *Adapter) videoMeta(ctx context.Context, aid int64, page int) (meta, error) {
	target := fmt.Sprintf("/x/web-interface/view?aid=%d", aid)
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
	title := strings.TrimSpace(data.Title)
	if part := strings.TrimSpace(p.Part); len(data.Pages) > 1 && part != "" {
		title += " / " + part
	}
	return meta{cid: p.CID, title: title, duration: p.Duration}, nil
}
