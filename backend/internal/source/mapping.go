package source

import "slices"

// duplicateNumber 同一个合集里序号相同的条目对不上的原因。
const duplicateNumber = "集号重复"

// Mapping 集号对应：合集第 From 集为本地第 To 集，之后一一顺延，序号小于 From 的不参与。都不小于 0。
type Mapping struct {
	From int
	To   int
}

// Episode 合集序号 n 对应的本地集号；n 在起点之前时 ok 为 false。
func (m Mapping) Episode(n int) (episode int, ok bool) {
	if n < m.From {
		return 0, false
	}
	return n - m.From + m.To, true
}

// MarkDuplicateNumbers 返回标出重复序号之后的条目：同一个合集里两个以上的条目序号相同时，这些条目都对不上，
// 原因为 duplicateNumber。已经对不上的条目不参与判定。与集号对应无关，不改动传入的切片。
func MarkDuplicateNumbers(items []CollectionItem) []CollectionItem {
	count := make(map[int]int)
	for _, it := range items {
		if it.Unmatched == "" {
			count[it.Number]++
		}
	}
	marked := make([]CollectionItem, len(items))
	for i, it := range items {
		if it.Unmatched == "" && count[it.Number] > 1 {
			it.Number, it.Unmatched = 0, duplicateNumber
		}
		marked[i] = it
	}
	return marked
}

// NormalizeItems 预览、保存之前整理 ListCollection 列出的条目：ref 相同的条目只保留第一个
// （季绑定的条目与处理过的记录都按弹幕源区分），再用 MarkDuplicateNumbers 标出重复的序号。不改动传入的切片。
func NormalizeItems(items []CollectionItem) []CollectionItem {
	seen := make(map[string]bool, len(items))
	unique := make([]CollectionItem, 0, len(items))
	for _, it := range items {
		if !seen[string(it.Ref)] {
			seen[string(it.Ref)] = true
			unique = append(unique, it)
		}
	}
	return MarkDuplicateNumbers(unique)
}

// DefaultMapping 预览给出的默认集号对应。取合集里最小的有效序号 m（没有有效序号时为 1）：
// 本季有第 m 集时同号对应，否则对到本季最小的集号；本季没有集时对到第 1 集。episodes 是本季的集号，顺序不限。
func DefaultMapping(items []CollectionItem, episodes []int) Mapping {
	m, found := 0, false
	for _, it := range MarkDuplicateNumbers(items) {
		if it.Unmatched == "" && (!found || it.Number < m) {
			m, found = it.Number, true
		}
	}
	if !found {
		m = 1
	}
	switch {
	case len(episodes) == 0:
		return Mapping{From: m, To: 1}
	case slices.Contains(episodes, m):
		return Mapping{From: m, To: m}
	default:
		return Mapping{From: m, To: slices.Min(episodes)}
	}
}
