package service

import (
	"fmt"
	"time"

	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/dao"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// View 季绑定的 JSON（剧详情里的 seasonBindings[]）：两种都有的字段，加上只属于合集的（*CollectionView）或只属于文件夹的
// （*FolderView），另一种为 nil，它的字段不出现在 JSON 里。不对外暴露原始的合集 ref。
type View struct {
	ID       int64  `json:"id"`
	SeasonID int64  `json:"seasonId"`
	Kind     string `json:"kind"`  // collection | folder
	Title    string `json:"title"` // 合集的季绑定为合集标题，每次列出时更新；文件夹的季绑定为上传的文件夹名
	// EpisodePatterns 集号规则：一组正则，取法见 source.EpisodeRule
	EpisodePatterns []string                 `json:"episodePatterns"`
	EpisodeRule     source.EpisodeRuleConfig `json:"episodeRule"`
	MappingFrom     int32                    `json:"mappingFrom"` // 集号对应：条目第 mappingFrom 集为本地第 mappingTo 集
	MappingTo       int32                    `json:"mappingTo"`
	BindingCount    int32                    `json:"bindingCount"` // 它建出的、现存的绑定数
	// CreatedAt 创建时间：文件夹的季绑定为第一次上传的时间
	CreatedAt time.Time `json:"createdAt"`

	*CollectionView
	*FolderView
}

// CollectionView 只属于合集的季绑定的字段。sourceUrl / sourceLabel 由适配器的 DescribeCollection 生成。
type CollectionView struct {
	Adapter     string `json:"adapter"`
	Link        string `json:"link"` // 创建或保存时贴的链接，编辑时预填
	SourceURL   string `json:"sourceUrl"`
	SourceLabel string `json:"sourceLabel"` // 例如"B 站番剧 ss41410"，含合集的种类
	// NumberedByRule 合集的序号由集号规则从条目的标签认出（投稿合集、多 P 投稿），上次列出时由适配器给出
	NumberedByRule bool       `json:"numberedByRule"`
	Follow         bool       `json:"follow"`
	Status         string     `json:"status"`        // active | dead
	LastError      *string    `json:"lastError"`     // 上次检查结束时的错误
	LastCheckedAt  *time.Time `json:"lastCheckedAt"` // 上次检查的开始时间
	Running        bool       `json:"running"`       // 正在补建
}

// FolderView 只属于文件夹的季绑定的字段。
type FolderView struct {
	EntryCount int32 `json:"entryCount"` // 保存的条目数
}

// Detail 季绑定的详情：items 是各条目按补建的语义派生的结果（不删除），emptySlots 是没有条目对到、绑定还留着的槽位。
type Detail struct {
	View
	Items      []ItemView `json:"items"`
	EmptySlots []SlotView `json:"emptySlots"`
}

// ItemView 一个条目派生的结果（Item 的 JSON），详情和预览共用。
type ItemView struct {
	Label         string               `json:"label"`         // 文件夹的条目为"季绑定标题 / 条目名称"
	Number        *int                 `json:"number"`        // 序号；对不上时为 null
	EpisodeNumber *int                 `json:"episodeNumber"` // 对到的本地集号；对不上、在起点之前时为 null
	Status        Status               `json:"status"`
	Reason        *string              `json:"reason"`   // 对不上的原因
	Replaces      *SlotView            `json:"replaces"` // 替换时为槽位里原来的绑定
	ReasonCode    string               `json:"reasonCode,omitempty"`
	Trace         *source.EpisodeTrace `json:"trace,omitempty"`
	// ItemError 只在详情里有（文件夹的恒为 null）；预览里为 nil，不出现在 JSON 里
	*ItemError
}

// ItemError 合集的条目上次补建失败的原因与时间，成功后清掉。
type ItemError struct {
	LastError   *string    `json:"lastError"`
	LastErrorAt *time.Time `json:"lastErrorAt"`
}

// SlotView 季绑定在一集上的槽位，以及放在里面的、它建出的绑定。
type SlotView struct {
	EpisodeNumber int    `json:"episodeNumber"`
	BindingID     int64  `json:"bindingId"`
	Title         string `json:"title"` // 绑定的标题
	Dead          bool   `json:"dead"`  // 绑定已失效
}

// Preview 预览：合集按候选各一组，文件夹只有一组。不保存任何东西。
type Preview struct {
	Candidates []Candidate `json:"candidates"`
}

// Candidate 预览的一组：合集的一个候选，或文件夹。Items、Removed、Summary 由 newCandidate 从派生的结果填好，其余由调用方填。
type Candidate struct {
	CollectionKind *string `json:"collectionKind"` // 合集的种类，保存时传回来选候选；文件夹为 null
	Title          string  `json:"title"`
	SourceURL      *string `json:"sourceUrl"`   // 文件夹为 null
	SourceLabel    *string `json:"sourceLabel"` // 文件夹为 null
	Finished       bool    `json:"finished"`    // 合集在平台上已完结
	NumberedByRule bool    `json:"numberedByRule"`
	Current        bool    `json:"current"` // 编辑时标出保存的候选

	Items   []ItemView `json:"items"`
	Removed []SlotView `json:"removed"` // 保存后删除的槽位
	Summary Summary    `json:"summary"`
}

// Summary 保存后各有几个：新建、替换、删除（没有条目的槽位）、保留。
type Summary struct {
	Create  int `json:"create"`
	Replace int `json:"replace"`
	Remove  int `json:"remove"`
	Keep    int `json:"keep"`
}

// newCandidate 按派生的结果填好预览的一组里的条目、要删的槽位和汇总，其余字段由调用方填。
func newCandidate(r Result) Candidate {
	c := Candidate{Items: itemViews(r.Items), Removed: slotViews(r.Empty)}
	for _, it := range r.Items {
		switch it.Status {
		case StatusCreate:
			c.Summary.Create++
		case StatusReplace:
			c.Summary.Replace++
		case StatusKeep:
			c.Summary.Keep++
		}
	}
	c.Summary.Remove = len(r.Empty)
	return c
}

// itemViews 派生的条目转为 JSON：对不上的带原因，对到了集的（含目录里没有这一集的）带集号，替换的带原来的槽位。
func itemViews(items []Item) []ItemView {
	views := make([]ItemView, len(items))
	for i, it := range items {
		v := ItemView{Label: it.Entry.Label, Status: it.Status, ReasonCode: it.Entry.ReasonCode, Trace: it.Entry.Trace}
		switch it.Status {
		case StatusUnmatched:
			v.Reason = new(it.Entry.Unmatched)
		case StatusBeforeStart:
			v.Number = new(it.Entry.Number)
		default:
			v.Number, v.EpisodeNumber = new(it.Entry.Number), new(it.Episode)
		}
		if it.Replaces != nil {
			v.Replaces = new(slotView(it.Episode, *it.Replaces))
		}
		views[i] = v
	}
	return views
}

// slotViews 没有条目对到的槽位转为 JSON，没有时为空数组。
func slotViews(empty []EmptySlot) []SlotView {
	views := make([]SlotView, len(empty))
	for i, e := range empty {
		views[i] = slotView(e.Episode, e.Slot)
	}
	return views
}

func slotView(episode int, s Slot) SlotView {
	return SlotView{EpisodeNumber: episode, BindingID: s.BindingID, Title: s.Title, Dead: s.Dead}
}

// view 季绑定的 JSON。合集的季绑定的链接和标签交给它的适配器生成；文件夹的季绑定没有合集，不经过适配器。
// entryCount 只用于文件夹，running 只用于合集。
func (s *Service) view(sb dao.SeasonBinding, bindingCount, entryCount int32, running bool) (View, error) {
	rule, err := formRule(sb)
	if err != nil {
		return View{}, err
	}
	v := View{
		ID:              sb.ID,
		SeasonID:        sb.SeasonID,
		Kind:            sb.Kind,
		Title:           sb.Title,
		EpisodePatterns: rule.Patterns(),
		EpisodeRule:     rule.Config(),
		MappingFrom:     sb.MappingFrom,
		MappingTo:       sb.MappingTo,
		BindingCount:    bindingCount,
		CreatedAt:       sb.CreatedAt,
	}
	if sb.Kind == KindFolder {
		v.FolderView = &FolderView{EntryCount: entryCount}
		return v, nil
	}
	adapter, err := s.sources.Get(*sb.Adapter)
	if err != nil {
		return View{}, fmt.Errorf("season binding %d: %w", sb.ID, err)
	}
	d, err := adapter.DescribeCollection(sb.Ref)
	if err != nil {
		return View{}, fmt.Errorf("describe season binding %d: %w", sb.ID, err)
	}
	v.CollectionView = &CollectionView{
		Adapter:        *sb.Adapter,
		Link:           emptyIfNull(sb.Link),
		SourceURL:      d.URL,
		SourceLabel:    d.Label,
		NumberedByRule: sb.NumberedByRule != nil && *sb.NumberedByRule,
		Follow:         sb.Follow,
		Status:         sb.Status,
		LastError:      sb.LastError,
		LastCheckedAt:  sb.LastCheckedAt,
		Running:        running,
	}
	return v, nil
}
