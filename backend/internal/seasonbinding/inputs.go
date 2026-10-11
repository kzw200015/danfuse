package seasonbinding

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/seasonbindingdb"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// 派生（Derive）的输入从哪里来（规格第 3 节"各处的输入"）：
//   - 条目：合集为 source.NumberItems 的输出（collectionEntries），预览、保存、补建用这次列出的合集，
//     详情用上次列出的结果（storedCollection）；文件夹为 folderEntries。
//   - 集与槽位：episodeSlots。预览和详情在读取时查（q 为池上的），保存和补建在写入事务里查（q 为事务里的）。
//   - 集号对应与集号规则：表单里的（formRule、formMapping），预览时取请求里的。

// derivedInput 一次派生的全部输入，连同合集的条目上次补建失败的原因（按 Entry.Source，详情用）。
type derivedInput struct {
	entries  []Entry
	mapping  source.Mapping
	episodes map[int]Episode
	errors   map[string]ItemError
}

// storedInput 按季绑定保存的表单和上次列出的结果（合集）、保存的条目（文件夹）组装派生的输入，详情用。q 可以在事务里。
func storedInput(ctx context.Context, q *seasonbindingdb.Queries, sb seasonbindingdb.SeasonBinding) (derivedInput, error) {
	rule, err := formRule(sb)
	if err != nil {
		return derivedInput{}, err
	}
	in := derivedInput{mapping: formMapping(sb)}
	adapter := ""
	switch sb.Kind {
	case kindCollection:
		adapter = *sb.Adapter
		rows, err := q.ListSeasonBindingItems(ctx, sb.ID)
		if err != nil {
			return derivedInput{}, fmt.Errorf("list items of season binding %d: %w", sb.ID, err)
		}
		in.entries = collectionEntries(source.NumberItems(storedCollection(sb, rows), rule))
		in.errors = make(map[string]ItemError, len(rows))
		for _, r := range rows {
			in.errors[source.CanonicalRef(r.Ref)] = ItemError{LastError: r.LastError, LastErrorAt: r.LastErrorAt}
		}
	case kindFolder:
		rows, err := q.ListSeasonBindingEntries(ctx, sb.ID)
		if err != nil {
			return derivedInput{}, fmt.Errorf("list entries of season binding %d: %w", sb.ID, err)
		}
		saved := make([]folderEntry, len(rows))
		for i, r := range rows {
			saved[i] = folderEntry{Source: entrySource(r.ID), Label: r.Label}
		}
		in.entries = folderEntries(sb.Title, saved, rule)
	}
	in.episodes, err = episodeSlots(ctx, q, sb.SeasonID, &sb.ID, adapter)
	return in, err
}

// formRule 季绑定保存的集号规则。存进去之前校验过，解析失败是服务器内部错误。
func formRule(sb seasonbindingdb.SeasonBinding) (source.EpisodeRule, error) {
	rule, err := source.ParseEpisodeRule(sb.EpisodePatterns)
	if err != nil {
		return source.EpisodeRule{}, fmt.Errorf("parse episode patterns of season binding %d: %w", sb.ID, err)
	}
	return rule, nil
}

// formMapping 季绑定保存的集号对应。
func formMapping(sb seasonbindingdb.SeasonBinding) source.Mapping {
	return source.Mapping{From: int(sb.MappingFrom), To: int(sb.MappingTo)}
}

// collectionEntries 合集的条目（source.NumberItems 的输出）转为派生的输入：弹幕源的身份为规范化的 ref
// （source.CanonicalRef，也能原样交给适配器 Fetch）。
func collectionEntries(items []source.CollectionItem) []Entry {
	entries := make([]Entry, len(items))
	for i, it := range items {
		entries[i] = Entry{Source: source.CanonicalRef(it.Ref), Label: it.Label, Number: it.Number, Unmatched: it.Unmatched}
	}
	return entries
}

// noPlatformNumber 不按规则编号的合集（番剧），上次列出时平台没有给出整数集号的条目对不上的原因。
const noPlatformNumber = "平台没有给出整数集号"

// storedCollection 合集上次列出的结果（season_binding_items，按位置排列）拼回 source.Collection，再交给 source.NumberItems 编号：
// 按规则编号的合集由集号规则从标签认出，其余用保存的平台集号（为空时对不上）。
func storedCollection(sb seasonbindingdb.SeasonBinding, rows []seasonbindingdb.SeasonBindingItem) source.Collection {
	col := source.Collection{NumberedByRule: sb.NumberedByRule != nil && *sb.NumberedByRule, Items: make([]source.CollectionItem, len(rows))}
	for i, r := range rows {
		it := source.CollectionItem{Ref: r.Ref, Label: r.Label}
		switch {
		case col.NumberedByRule:
		case r.Number == nil:
			it.Unmatched = noPlatformNumber
		default:
			it.Number = int(*r.Number)
		}
		col.Items[i] = it
	}
	return col
}

// folderEntry 文件夹的一个条目，交给 folderEntries：Source 是它的身份（保存的条目为 entrySource(条目 ID)；
// 换文件夹时没对上保存的条目的，为临时身份 "new:<名称>"，不会与任何槽位相同），Label 是条目名称（不带文件夹名）。
type folderEntry struct {
	Source string
	Label  string
}

// folderEntries 文件夹的条目转为派生的输入：按"季绑定标题 / 条目名称"（source.LabelSeparator，和多 P 投稿的"稿件标题 / 分 P 标题"一致）
// 用集号规则认集号（source.NumberLabels），派生的条目名称也是拼好的。title 为季绑定的标题：新建、换文件夹时为这次的文件夹名。
func folderEntries(title string, entries []folderEntry, rule source.EpisodeRule) []Entry {
	labels := make([]string, len(entries))
	for i, e := range entries {
		labels[i] = title + source.LabelSeparator + e.Label
	}
	numbered := source.NumberLabels(labels, rule)
	out := make([]Entry, len(entries))
	for i, it := range numbered {
		out[i] = Entry{Source: entries[i].Source, Label: it.Label, Number: it.Number, Unmatched: it.Unmatched}
	}
	return out
}

// entrySource 保存的条目作为弹幕源的身份（Entry.Source、文件绑定的 Slot.Source）。
func entrySource(entryID int64) string {
	return strconv.FormatInt(entryID, 10)
}

// episodeSlots 派生的输入 episodes（键为集号）：一季的全部集，连同季绑定 seasonBindingID 在各集的槽位
// （为 nil 时，例如新建的预览，没有槽位）；adapter 不为空（合集）时另查各集上别的绑定占用的、这个适配器的弹幕源。
// 链接绑定的弹幕源为规范化的 ref，文件绑定的为条目 ID。
func episodeSlots(ctx context.Context, q *seasonbindingdb.Queries, seasonID int64, seasonBindingID *int64, adapter string) (map[int]Episode, error) {
	rows, err := q.ListSeasonEpisodeSlots(ctx, seasonbindingdb.ListSeasonEpisodeSlotsParams{SeasonID: seasonID, SeasonBindingID: seasonBindingID})
	if err != nil {
		return nil, fmt.Errorf("list episodes of season %d: %w", seasonID, err)
	}
	episodes := make(map[int]Episode, len(rows))
	for _, r := range rows {
		ep := Episode{ID: r.ID}
		if r.BindingID != nil {
			slot := Slot{BindingID: *r.BindingID, Title: emptyIfNull(r.BindingTitle), Dead: emptyIfNull(r.BindingStatus) == "dead"}
			if r.BindingEntryID != nil {
				slot.Source = entrySource(*r.BindingEntryID)
			} else {
				slot.Source = source.CanonicalRef(r.BindingRef)
			}
			ep.Slot = &slot
		}
		episodes[int(r.Number)] = ep
	}
	if adapter == "" {
		return episodes, nil
	}
	occupied, err := q.ListSeasonOccupiedSources(ctx, seasonbindingdb.ListSeasonOccupiedSourcesParams{
		SeasonID: seasonID, Adapter: adapter, SeasonBindingID: seasonBindingID,
	})
	if err != nil {
		return nil, fmt.Errorf("list occupied sources of season %d: %w", seasonID, err)
	}
	for _, o := range occupied {
		ep := episodes[int(o.Number)]
		if ep.Occupied == nil {
			ep.Occupied = make(map[string]bool)
		}
		ep.Occupied[source.CanonicalRef(o.Ref)] = true
		episodes[int(o.Number)] = ep
	}
	return episodes, nil
}
