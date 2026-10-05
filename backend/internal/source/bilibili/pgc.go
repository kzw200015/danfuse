package bilibili

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/source"
)

// seasonData pgc/view/web/season 的 result：按 ep_id 查询时返回这一集所在的一季，也可以按 season_id 查询。只取用到的字段。
type seasonData struct {
	SeasonID int64        `json:"season_id"`
	Title    string       `json:"title"` // 番剧名，例如"某某 第二季"
	Publish  pgcPublish   `json:"publish"`
	Episodes []pgcEpisode `json:"episodes"` // 正片，也可能混有预告（section_type 不为 0）
	Section  []pgcSection `json:"section"`
}

// pgcPublish 番剧的播出状态。
type pgcPublish struct {
	IsFinish int `json:"is_finish"` // 1 表示已完结
}

// pgcSection 正片以外的一组单集，例如 PV、花絮。
type pgcSection struct {
	Episodes []pgcEpisode `json:"episodes"`
}

// pgcEpisode 番剧的一集。
type pgcEpisode struct {
	ID        int64  `json:"id"` // ep_id
	CID       int64  `json:"cid"`
	Title     string `json:"title"`      // 集号，例如"1""PV"
	LongTitle string `json:"long_title"` // 集标题
	ShowTitle string `json:"show_title"` // 例如"第1话 集标题"
	Duration  int64  `json:"duration"`   // 毫秒，与投稿的秒不同
	// SectionType 0 为正片（包括 SP、OAD、24.9 这类特别篇）；混在 episodes 里的预告为 1，标题与对应的正片同号
	SectionType int `json:"section_type"`
}

// season 取 epID 所在的季。
func (a *Adapter) season(ctx context.Context, epID int64) (seasonData, error) {
	var s seasonData
	err := a.client.getJSON(ctx, fmt.Sprintf("%s/pgc/view/web/season?ep_id=%d", apiURL, epID), &s)
	return s, err
}

// episodeMeta 取番剧单集的 cid、标题和时长。正片和 section 里的 PV、花絮等都能绑定；ep 不在这一季的列表里为 NotFound。
func (a *Adapter) episodeMeta(ctx context.Context, epID int64) (meta, error) {
	s, err := a.season(ctx, epID)
	if err != nil {
		return meta{}, err
	}
	all := s.allEpisodes()
	i := slices.IndexFunc(all, func(e pgcEpisode) bool { return e.ID == epID })
	if i < 0 {
		return meta{}, sourceError(source.NotFound, fmt.Errorf("ep%d 不在 ss%d 的单集列表里", epID, s.SeasonID))
	}
	return s.meta(all[i])
}

// allEpisodes 正片 episodes 之后接上 section[] 里的各集。
func (s seasonData) allEpisodes() []pgcEpisode {
	all := slices.Clone(s.Episodes)
	for _, sec := range s.Section {
		all = append(all, sec.Episodes...)
	}
	return all
}

// meta 这一季里的一集：标题为"番剧名 集标题"（见 label）；时长从毫秒四舍五入到秒。
func (s seasonData) meta(e pgcEpisode) (meta, error) {
	if e.CID <= 0 {
		return meta{}, sourceError(source.Upstream, fmt.Errorf("ep%d 没有 cid", e.ID))
	}
	return meta{cid: e.CID, title: joinNonEmpty(s.Title, e.label()), duration: int((e.Duration + 500) / 1000)}, nil
}

// label 集标题：优先用 show_title（如"第1话 集标题"），没有时用集号加集标题。
func (e pgcEpisode) label() string {
	if strings.TrimSpace(e.ShowTitle) != "" {
		return strings.TrimSpace(e.ShowTitle)
	}
	return joinNonEmpty(e.Title, e.LongTitle)
}

// joinNonEmpty 去掉各部分首尾的空白，用空格连接不为空的部分。
func joinNonEmpty(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}
