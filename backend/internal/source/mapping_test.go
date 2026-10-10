package source

import (
	"reflect"
	"testing"
)

func TestMappingEpisode(t *testing.T) {
	tests := []struct {
		name    string
		mapping Mapping
		number  int
		want    int
		wantOK  bool
	}{
		{"同号对应", Mapping{From: 1, To: 1}, 3, 3, true},
		{"起点本身", Mapping{From: 14, To: 1}, 14, 1, true},
		{"起点之后顺延", Mapping{From: 14, To: 1}, 25, 12, true},
		{"在起点之前", Mapping{From: 14, To: 1}, 13, 0, false},
		{"本地从后面接上", Mapping{From: 1, To: 13}, 12, 24, true},
		{"合集序号为 0", Mapping{From: 0, To: 1}, 0, 1, true},
		{"对到第 0 集", Mapping{From: 2, To: 0}, 2, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tt.mapping.Episode(tt.number)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("%+v.Episode(%d) = (%d, %v), want (%d, %v)", tt.mapping, tt.number, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// numbered 一个有序号的条目；unmatched 一个对不上的条目。
func numbered(label string, n int) CollectionItem {
	return CollectionItem{Ref: Ref(`"` + label + `"`), Number: n, Label: label}
}

func unmatched(label, reason string) CollectionItem {
	return CollectionItem{Ref: Ref(`"` + label + `"`), Unmatched: reason, Label: label}
}

func TestMarkDuplicateNumbers(t *testing.T) {
	tests := []struct {
		name  string
		items []CollectionItem
		want  []CollectionItem
	}{
		{"没有重复", []CollectionItem{numbered("a", 1), numbered("b", 2)}, []CollectionItem{numbered("a", 1), numbered("b", 2)}},
		{
			"两条同号：两条都对不上，与位置无关",
			[]CollectionItem{numbered("a", 1), numbered("b", 3), numbered("c", 2), numbered("d", 3)},
			[]CollectionItem{numbered("a", 1), unmatched("b", "集号重复"), numbered("c", 2), unmatched("d", "集号重复")},
		},
		{
			"已经对不上的条目不参与判定，原因不变",
			[]CollectionItem{unmatched("a", "集号「SP」不是整数"), unmatched("b", "集号「SP」不是整数"), numbered("c", 0)},
			[]CollectionItem{unmatched("a", "集号「SP」不是整数"), unmatched("b", "集号「SP」不是整数"), numbered("c", 0)},
		},
		{"空列表", nil, []CollectionItem{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := append([]CollectionItem(nil), tt.items...)
			got := markDuplicateNumbers(tt.items)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("markDuplicateNumbers() = %+v\nwant %+v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.items, in) {
				t.Errorf("改动了传入的切片：%+v", tt.items)
			}
		})
	}
}

func TestNormalizeItems(t *testing.T) {
	tests := []struct {
		name  string
		items []CollectionItem
		want  []CollectionItem
	}{
		{"没有重复", []CollectionItem{numbered("a", 1), numbered("b", 2)}, []CollectionItem{numbered("a", 1), numbered("b", 2)}},
		{
			"ref 重复：只保留第一个，后面的不算重复的序号",
			[]CollectionItem{numbered("a", 1), numbered("b", 2), {Ref: Ref(`"a"`), Number: 3, Label: "a 的另一处"}},
			[]CollectionItem{numbered("a", 1), numbered("b", 2)},
		},
		{
			"去掉重复的 ref 之后才判定序号",
			[]CollectionItem{numbered("a", 1), numbered("a", 1), numbered("b", 2)},
			[]CollectionItem{numbered("a", 1), numbered("b", 2)},
		},
		{"空列表", nil, []CollectionItem{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := append([]CollectionItem(nil), tt.items...)
			got := normalizeItems(tt.items)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("normalizeItems() = %+v\nwant %+v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.items, in) {
				t.Errorf("改动了传入的切片：%+v", tt.items)
			}
		})
	}
}
