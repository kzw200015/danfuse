package seasonbinding

import (
	"cmp"
	"slices"

	"github.com/kzw200015/danfuse/backend/internal/source"
)

// Entry 已编好号的条目，取自 source.NumberItems / NumberLabels 的输出。
type Entry struct {
	Source    string // 弹幕源的身份：合集为 source.CanonicalRef(ref)；文件夹为条目 ID
	Label     string
	Number    int    // Unmatched 为空时才有意义
	Unmatched string // 非空表示对不上，内容是 source 给出的原因
}

// Episode 本季的一集，连同这个季绑定在这一集上的槽位。
type Episode struct {
	ID       int64
	Slot     *Slot           // 这个季绑定在这一集建出的绑定；没有时为 nil
	Occupied map[string]bool // 合集：这一集上别的绑定占用的弹幕源，只算这个季绑定的适配器；文件夹为 nil
}

// Slot 这个季绑定在一集上建出的绑定。
type Slot struct {
	BindingID int64
	Source    string // 同 Entry.Source：链接绑定为规范化的 ref，文件绑定为 entry_id
	Title     string
	Dead      bool
}

// Status 条目派生出的状态，取值与管理 API 里条目的 status 相同。
type Status string

const (
	StatusKeep    Status = "keep"    // 槽位里是同一个弹幕源，保留
	StatusCreate  Status = "create"  // 槽位为空，新建
	StatusReplace Status = "replace" // 槽位里是别的弹幕源，替换（删旧建新）
	// StatusAlreadyBound 那一集上别的绑定（手动绑的、别的季绑定建的）占着这个弹幕源，跳过；槽位不动，不算空
	StatusAlreadyBound Status = "alreadyBound"

	StatusUnmatched   Status = "unmatched"   // 对不上，原因见 Entry.Unmatched
	StatusBeforeStart Status = "beforeStart" // 序号在集号对应的起点之前
	StatusNoEpisode   Status = "noEpisode"   // 目录里没有对到的这一集
)

// Item 一个条目派生的结果。
type Item struct {
	Entry     Entry
	Status    Status
	Episode   int   // 对到的本地集号；对不上、在起点之前时没有意义（为 0）
	EpisodeID int64 // 对到的集；对不上、在起点之前、目录里没有这一集时为 0
	Replaces  *Slot // 替换时为原来的槽位，其余为 nil
}

// EmptySlot 没有条目对到的槽位，例如稿件下架、条目改对到了别的集之后留下的。
type EmptySlot struct {
	Episode   int // 槽位所在的集号
	EpisodeID int64
	Slot      Slot
}

// Result 派生的结果。
type Result struct {
	Items []Item      // 与 entries 一一对应
	Empty []EmptySlot // 现有的槽位里没有条目对到的，按集号排列；保存时删除，补建时留着。没有时为 nil
}

// Derive 按集号对应把条目对到本季的各集，与这个季绑定的槽位比较，算出每个条目的状态和没有条目对到的槽位。
// 纯函数，只管集号对应和槽位：编号、认不出集号、集号重复、去掉重复的弹幕源都由 source 事先做好，
// 所以对到同一集的条目至多一个。episodes 的键为集号。
func Derive(entries []Entry, m source.Mapping, episodes map[int]Episode) Result {
	items := make([]Item, len(entries))
	claimed := make(map[int]bool, len(entries)) // 有条目对到的集
	for i, e := range entries {
		items[i] = deriveItem(e, m, episodes)
		switch items[i].Status {
		case StatusKeep, StatusCreate, StatusReplace, StatusAlreadyBound:
			claimed[items[i].Episode] = true
		}
	}
	var empty []EmptySlot
	for n, ep := range episodes {
		if ep.Slot != nil && !claimed[n] {
			empty = append(empty, EmptySlot{Episode: n, EpisodeID: ep.ID, Slot: *ep.Slot})
		}
	}
	slices.SortFunc(empty, func(a, b EmptySlot) int { return cmp.Compare(a.Episode, b.Episode) })
	return Result{Items: items, Empty: empty}
}

// deriveItem 依次判断一个条目：对不上、在起点之前、目录里没有这一集，再看那一集：已有同源绑定、槽位为空、同源保留、不同源替换。
func deriveItem(e Entry, m source.Mapping, episodes map[int]Episode) Item {
	if e.Unmatched != "" {
		return Item{Entry: e, Status: StatusUnmatched}
	}
	n, ok := m.Episode(e.Number)
	if !ok {
		return Item{Entry: e, Status: StatusBeforeStart}
	}
	ep, ok := episodes[n]
	if !ok {
		return Item{Entry: e, Status: StatusNoEpisode, Episode: n}
	}
	item := Item{Entry: e, Episode: n, EpisodeID: ep.ID}
	switch {
	case ep.Occupied[e.Source]:
		item.Status = StatusAlreadyBound
	case ep.Slot == nil:
		item.Status = StatusCreate
	case ep.Slot.Source == e.Source:
		item.Status = StatusKeep
	default:
		replaced := *ep.Slot
		item.Status, item.Replaces = StatusReplace, &replaced
	}
	return item
}
