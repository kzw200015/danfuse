package bilibili

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kzw200015/danfuse/backend/internal/source"
)

// seasonData pgc/view/web/season 的 result：按 ep_id 查询，返回这一集所在的整季，只取用到的字段。
type seasonData struct {
	SeasonID int64        `json:"season_id"`
	Title    string       `json:"title"` // 番剧名，例如"某某 第二季"
	Episodes []pgcEpisode `json:"episodes"`
	Section  []pgcSection `json:"section"`
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
}

// episodeNotFound 番剧的 NotFound。从大陆请求港澳台限定的番剧时，pgc 与 ep 不存在时一样返回 -404，分不出来，
// 所以番剧的 NotFound 提示都带上"港澳台限定番剧暂不支持"。
func episodeNotFound(err error) *source.Error {
	return &source.Error{Kind: source.NotFound, Message: messages[source.NotFound] + "；港澳台限定番剧暂不支持", Err: err}
}

// season 取 epID 所在的整季。NotFound 见 episodeNotFound。
func (a *Adapter) season(ctx context.Context, epID int64) (seasonData, error) {
	var s seasonData
	err := a.client.getJSON(ctx, fmt.Sprintf("%s/pgc/view/web/season?ep_id=%d", apiURL, epID), &s)
	if srcErr, ok := errors.AsType[*source.Error](err); ok && srcErr.Kind == source.NotFound {
		return seasonData{}, episodeNotFound(srcErr.Err)
	}
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
		return meta{}, episodeNotFound(fmt.Errorf("ep%d 不在 ss%d 的单集列表里", epID, s.SeasonID))
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

// meta 这一季里的一集：标题为"番剧名 集标题"，集标题优先用 show_title（如"第1话 集标题"），没有时用集号加集标题；
// 时长从毫秒四舍五入到秒。
func (s seasonData) meta(e pgcEpisode) (meta, error) {
	if e.CID <= 0 {
		return meta{}, sourceError(source.Upstream, fmt.Errorf("ep%d 没有 cid", e.ID))
	}
	parts := []string{s.Title, e.ShowTitle}
	if strings.TrimSpace(e.ShowTitle) == "" {
		parts = []string{s.Title, e.Title, e.LongTitle}
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	title := strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), " ")
	return meta{cid: e.CID, title: title, duration: int((e.Duration + 500) / 1000)}, nil
}
