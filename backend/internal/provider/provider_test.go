package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// fakeLocal 假的本地 Provider：搜索原样返回 result，取季返回带着 id 的一季，取弹幕返回一条带着 episodeID 的弹幕。
type fakeLocal struct {
	result SearchResult
	query  SearchQuery // 收到的搜索条件
}

func (f *fakeLocal) Search(_ context.Context, q SearchQuery) (SearchResult, error) {
	f.query = q
	return f.result, nil
}

func (f *fakeLocal) Season(_ context.Context, id int64) (Season, bool, error) {
	return Season{ID: id}, true, nil
}

func (f *fakeLocal) Comments(_ context.Context, episodeID int64) ([]danmaku.Item, error) {
	return []danmaku.Item{{SourceID: episodeID}}, nil
}

func TestAggregatorSearchAsksLocal(t *testing.T) {
	local := &fakeLocal{result: SearchResult{Seasons: []Season{{ID: 1, Name: "星海旅人"}}, HasMore: true}}
	q := SearchQuery{Keyword: "星海旅人", MaxSeasons: 50}

	got, err := NewAggregator(local).Search(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	if local.query != q {
		t.Errorf("本地收到 %+v, want %+v", local.query, q)
	}
	if !reflect.DeepEqual(got, local.result) {
		t.Errorf("Search() = %+v, want 本地的结果 %+v", got, local.result)
	}
}

// TestAggregatorMatch 名称里要写明集号，原样交给本地搜索；候选唯一、且标题与剧名或原名相同才算确定。
func TestAggregatorMatch(t *testing.T) {
	starSea := Season{ID: 1, Name: "星海旅人", Titles: []string{"星海旅人", "ほしうみの旅人"}}
	special := Season{ID: 3, Name: "星海旅人 特别篇", Titles: []string{"星海旅人", "ほしうみの旅人"}}
	tests := []struct {
		name       string
		seasons    []Season // 本地搜索的结果
		wantSearch bool
		wantExact  bool
	}{
		{"星海旅人 S01E01", []Season{starSea}, true, true},
		{"ほしうみの旅人 S01E01", []Season{starSea}, true, true},     // 原名
		{"星海旅人 第1话", []Season{starSea, special}, true, false}, // 候选不唯一
		{"旅人 S01E01", []Season{starSea}, true, false},         // 标题只是剧名中的一段
		{"星海旅人 S01E99", nil, true, false},                     // 没有候选
		{"星海旅人", []Season{starSea}, false, false},             // 没有写明集号，不搜索
		{"星海旅人 第2季", []Season{starSea}, false, false},         // 只有季号
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			local := &fakeLocal{result: SearchResult{Seasons: tt.seasons}}
			got, err := NewAggregator(local).Match(t.Context(), tt.name, 50)
			if err != nil {
				t.Fatal(err)
			}
			if searched := local.query != (SearchQuery{}); searched != tt.wantSearch {
				t.Fatalf("交给本地搜索 = %v, want %v", searched, tt.wantSearch)
			}
			if tt.wantSearch && local.query != (SearchQuery{Keyword: tt.name, MaxSeasons: 50}) {
				t.Errorf("本地收到 %+v, want 原样的名称", local.query)
			}
			wantCandidates := tt.seasons
			if !tt.wantSearch {
				wantCandidates = nil
			}
			if got.Exact != tt.wantExact || !reflect.DeepEqual(got.Candidates, wantCandidates) {
				t.Errorf("Match() = %+v, want 候选 %+v, Exact %v", got, wantCandidates, tt.wantExact)
			}
		})
	}
}

// routeTests 按 ID 号段路由的用例：本地号段内的交给本地 Provider，其余还没有 Provider。
var routeTests = []struct {
	name      string
	id        int64
	wantLocal bool
}{
	{"本地 ID", 42, true},
	{"本地 ID 的上限之内", LocalIDLimit - 1, true},
	{"虚拟 ID", LocalIDLimit, false},
	{"0", 0, false},
	{"负数", -1, false},
}

func TestAggregatorSeasonRoutesByID(t *testing.T) {
	for _, tt := range routeTests {
		t.Run(tt.name, func(t *testing.T) {
			got, found, err := NewAggregator(&fakeLocal{}).Season(t.Context(), tt.id)
			if err != nil {
				t.Fatal(err)
			}
			if fromLocal := found && got.ID == tt.id; fromLocal != tt.wantLocal {
				t.Errorf("Season(%d) = %+v, %v; want 交给本地 = %v", tt.id, got, found, tt.wantLocal)
			}
		})
	}
}

func TestAggregatorCommentsRoutesByID(t *testing.T) {
	for _, tt := range routeTests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewAggregator(&fakeLocal{}).Comments(t.Context(), tt.id)
			if err != nil {
				t.Fatal(err)
			}
			if fromLocal := len(got) == 1 && got[0].SourceID == tt.id; fromLocal != tt.wantLocal {
				t.Errorf("Comments(%d) = %+v, want 交给本地 = %v", tt.id, got, tt.wantLocal)
			}
		})
	}
}
