package service_test

import (
	"reflect"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/seasonbinding/service"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// numberedEntry 一个有序号的条目，弹幕源与标签同名。
func numberedEntry(src string, n int) service.Entry {
	return service.Entry{Source: src, Label: src, Number: n}
}

// boundSlot 第 bindingID 个绑定占着的槽位，弹幕源与标题同名。
func boundSlot(bindingID int64, src string) *service.Slot {
	return &service.Slot{BindingID: bindingID, Source: src, Title: src}
}

func TestDerive(t *testing.T) {
	same := source.Mapping{From: 1, To: 1}
	tests := []struct {
		name     string
		entries  []service.Entry
		mapping  source.Mapping
		episodes map[int]service.Episode
		want     service.Result
	}{
		{
			name:     "槽位为空：新建",
			entries:  []service.Entry{numberedEntry("a", 1)},
			mapping:  same,
			episodes: map[int]service.Episode{1: {ID: 101}},
			want: service.Result{Items: []service.Item{
				{Entry: numberedEntry("a", 1), Status: service.StatusCreate, Episode: 1, EpisodeID: 101},
			}},
		},
		{
			name:     "槽位里是同一个弹幕源：保留",
			entries:  []service.Entry{numberedEntry("a", 1)},
			mapping:  same,
			episodes: map[int]service.Episode{1: {ID: 101, Slot: boundSlot(11, "a")}},
			want: service.Result{Items: []service.Item{
				{Entry: numberedEntry("a", 1), Status: service.StatusKeep, Episode: 1, EpisodeID: 101},
			}},
		},
		{
			name:     "槽位里是别的弹幕源：替换，指向原来的槽位",
			entries:  []service.Entry{numberedEntry("b", 1)},
			mapping:  same,
			episodes: map[int]service.Episode{1: {ID: 101, Slot: boundSlot(11, "a")}},
			want: service.Result{Items: []service.Item{
				{Entry: numberedEntry("b", 1), Status: service.StatusReplace, Episode: 1, EpisodeID: 101, Replaces: boundSlot(11, "a")},
			}},
		},
		{
			name:     "对不上：原因原样输出，即使序号恰好能对到有槽位的集",
			entries:  []service.Entry{{Source: "a", Label: "SP", Unmatched: "集号「SP」不是整数"}},
			mapping:  source.Mapping{From: 0, To: 0},
			episodes: map[int]service.Episode{0: {ID: 100}},
			want: service.Result{Items: []service.Item{
				{Entry: service.Entry{Source: "a", Label: "SP", Unmatched: "集号「SP」不是整数"}, Status: service.StatusUnmatched},
			}},
		},
		{
			name:     "序号在起点之前",
			entries:  []service.Entry{numberedEntry("a", 13), numberedEntry("b", 14)},
			mapping:  source.Mapping{From: 14, To: 1},
			episodes: map[int]service.Episode{1: {ID: 101}},
			want: service.Result{Items: []service.Item{
				{Entry: numberedEntry("a", 13), Status: service.StatusBeforeStart},
				{Entry: numberedEntry("b", 14), Status: service.StatusCreate, Episode: 1, EpisodeID: 101},
			}},
		},
		{
			name:     "目录里没有对到的集：带上对到的集号",
			entries:  []service.Entry{numberedEntry("a", 2)},
			mapping:  source.Mapping{From: 1, To: 12},
			episodes: map[int]service.Episode{12: {ID: 112}},
			want: service.Result{Items: []service.Item{
				{Entry: numberedEntry("a", 2), Status: service.StatusNoEpisode, Episode: 13},
			}},
		},
		{
			name:     "别的绑定占着同一个弹幕源：已有同源绑定",
			entries:  []service.Entry{numberedEntry("a", 1)},
			mapping:  same,
			episodes: map[int]service.Episode{1: {ID: 101, Occupied: map[string]bool{"a": true}}},
			want: service.Result{Items: []service.Item{
				{Entry: numberedEntry("a", 1), Status: service.StatusAlreadyBound, Episode: 1, EpisodeID: 101},
			}},
		},
		{
			name:     "已有同源绑定时，槽位里别的弹幕源不替换，也不算空",
			entries:  []service.Entry{numberedEntry("a", 1)},
			mapping:  same,
			episodes: map[int]service.Episode{1: {ID: 101, Slot: boundSlot(11, "b"), Occupied: map[string]bool{"a": true}}},
			want: service.Result{Items: []service.Item{
				{Entry: numberedEntry("a", 1), Status: service.StatusAlreadyBound, Episode: 1, EpisodeID: 101},
			}},
		},
		{
			name:    "没有条目对到的槽位列为空槽位，按集号排列",
			entries: []service.Entry{numberedEntry("a", 2), {Source: "b", Label: "b", Unmatched: "集号重复"}},
			mapping: same,
			episodes: map[int]service.Episode{
				1: {ID: 101},
				2: {ID: 102, Slot: boundSlot(12, "a")},
				3: {ID: 103, Slot: &service.Slot{BindingID: 13, Source: "c", Title: "c", Dead: true}},
				4: {ID: 104, Slot: boundSlot(14, "b")},
			},
			want: service.Result{
				Items: []service.Item{
					{Entry: numberedEntry("a", 2), Status: service.StatusKeep, Episode: 2, EpisodeID: 102},
					{Entry: service.Entry{Source: "b", Label: "b", Unmatched: "集号重复"}, Status: service.StatusUnmatched},
				},
				Empty: []service.EmptySlot{
					{Episode: 3, EpisodeID: 103, Slot: service.Slot{BindingID: 13, Source: "c", Title: "c", Dead: true}},
					{Episode: 4, EpisodeID: 104, Slot: *boundSlot(14, "b")},
				},
			},
		},
		{
			name:     "条目改对到另一集：那一集新建，原来那一集变成空槽位",
			entries:  []service.Entry{numberedEntry("a", 1)},
			mapping:  source.Mapping{From: 1, To: 2},
			episodes: map[int]service.Episode{1: {ID: 101, Slot: boundSlot(11, "a")}, 2: {ID: 102}},
			want: service.Result{
				Items: []service.Item{{Entry: numberedEntry("a", 1), Status: service.StatusCreate, Episode: 2, EpisodeID: 102}},
				Empty: []service.EmptySlot{{Episode: 1, EpisodeID: 101, Slot: *boundSlot(11, "a")}},
			},
		},
		{
			name:     "稿件下架：槽位空着，绑定留在空槽位里",
			entries:  []service.Entry{numberedEntry("a", 4)},
			mapping:  same,
			episodes: map[int]service.Episode{4: {ID: 104, Slot: boundSlot(14, "a")}, 5: {ID: 105, Slot: boundSlot(15, "x")}},
			want: service.Result{
				Items: []service.Item{{Entry: numberedEntry("a", 4), Status: service.StatusKeep, Episode: 4, EpisodeID: 104}},
				Empty: []service.EmptySlot{{Episode: 5, EpisodeID: 105, Slot: *boundSlot(15, "x")}},
			},
		},
		{
			name:     "下架后重新投稿为同一集：替换下架的那个",
			entries:  []service.Entry{numberedEntry("a", 4), numberedEntry("y", 5)},
			mapping:  same,
			episodes: map[int]service.Episode{4: {ID: 104, Slot: boundSlot(14, "a")}, 5: {ID: 105, Slot: boundSlot(15, "x")}},
			want: service.Result{Items: []service.Item{
				{Entry: numberedEntry("a", 4), Status: service.StatusKeep, Episode: 4, EpisodeID: 104},
				{Entry: numberedEntry("y", 5), Status: service.StatusReplace, Episode: 5, EpisodeID: 105, Replaces: boundSlot(15, "x")},
			}},
		},
		{
			name:     "没有条目",
			entries:  nil,
			mapping:  same,
			episodes: map[int]service.Episode{1: {ID: 101}},
			want:     service.Result{Items: []service.Item{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := service.Derive(tt.entries, tt.mapping, tt.episodes)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Derive() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}
