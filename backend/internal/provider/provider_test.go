package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/danmaku"
)

// fakeLocal 假的本地 Provider：搜索原样返回 result，取弹幕返回一条带着 episodeID 的弹幕。
type fakeLocal struct {
	result SearchResult
	query  SearchQuery // 收到的搜索条件
}

func (f *fakeLocal) Search(_ context.Context, q SearchQuery) (SearchResult, error) {
	f.query = q
	return f.result, nil
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

func TestAggregatorCommentsRoutesByID(t *testing.T) {
	tests := []struct {
		name      string
		episodeID int64
		wantLocal bool
	}{
		{"本地 ID", 42, true},
		{"本地 ID 的上限之内", LocalIDLimit - 1, true},
		{"虚拟 ID", LocalIDLimit, false},
		{"0", 0, false},
		{"负数", -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewAggregator(&fakeLocal{}).Comments(t.Context(), tt.episodeID)
			if err != nil {
				t.Fatal(err)
			}
			if fromLocal := len(got) == 1 && got[0].SourceID == tt.episodeID; fromLocal != tt.wantLocal {
				t.Errorf("Comments(%d) = %+v, want 交给本地 = %v", tt.episodeID, got, tt.wantLocal)
			}
		})
	}
}
