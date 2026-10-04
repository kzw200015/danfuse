package catalog

import (
	"strings"
	"testing"
)

func TestSeriesValidate(t *testing.T) {
	episodes := func(numbers ...int) []Episode {
		eps := make([]Episode, len(numbers))
		for i, n := range numbers {
			eps[i] = Episode{Number: n}
		}
		return eps
	}
	tv := func(seasons ...Season) Series {
		return Series{Type: TypeTV, Title: "星海旅人", Seasons: seasons}
	}
	movie := func(seasons ...Season) Series {
		return Series{Type: TypeMovie, Title: "长夜灯塔", Seasons: seasons}
	}

	tests := []struct {
		name    string
		series  Series
		wantErr string // 为空表示通过
	}{
		{"剧集", tv(Season{Number: 0, Episodes: episodes(1)}, Season{Number: 1, Episodes: episodes(1, 2, 2)}), ""},
		{"集号可以为 0", tv(Season{Number: 1, Episodes: episodes(0)}), ""},
		{"电影", movie(Season{Number: 1, Episodes: episodes(1)}), ""},
		{"类型无效", Series{Type: "anime", Title: "星海旅人", Seasons: []Season{{Number: 1, Episodes: episodes(1)}}}, "类型"},
		{"标题为空", Series{Type: TypeTV, Title: " ", Seasons: []Season{{Number: 1, Episodes: episodes(1)}}}, "标题为空"},
		{"没有季", tv(), "没有任何季"},
		{"季里没有集", tv(Season{Number: 1}), "没有任何集"},
		{"季号为负", tv(Season{Number: -1, Episodes: episodes(1)}), "季号 -1"},
		{"集号为负", tv(Season{Number: 1, Episodes: episodes(1, -2)}), "集号 -2"},
		{"电影不是第 1 季", movie(Season{Number: 2, Episodes: episodes(1)}), "电影"},
		{"电影不是第 1 集", movie(Season{Number: 1, Episodes: episodes(2)}), "电影"},
		{"电影有两集", movie(Season{Number: 1, Episodes: episodes(1, 1)}), "电影"},
		{"电影有两季", movie(Season{Number: 1, Episodes: episodes(1)}, Season{Number: 2, Episodes: episodes(1)}), "电影"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.series.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("Validate() = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("Validate() = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}
