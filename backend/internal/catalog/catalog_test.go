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

// TestSeasonName 季的名称，以及 ParseName 能从它拆回剧名和季号：插件手动选过一季之后，拿这个名称作为关键词搜回同一季。
// 第 1 季和电影的名称就是剧名，没有季号。
func TestSeasonName(t *testing.T) {
	tests := []struct {
		typ        SeriesType
		number     int
		wantName   string
		wantParsed string // ParseName 拆出的"标题 | 季号 | 集号"
	}{
		{TypeTV, 1, "星海旅人", "星海旅人 | - | -"},
		{TypeTV, 2, "星海旅人 第2季", "星海旅人 | 2 | -"},
		{TypeTV, 12, "星海旅人 第12季", "星海旅人 | 12 | -"},
		{TypeTV, 0, "星海旅人 特别篇", "星海旅人 | 0 | -"},
		{TypeMovie, 1, "星海旅人", "星海旅人 | - | -"},
	}
	for _, tt := range tests {
		name := SeasonName(tt.typ, "星海旅人", tt.number)
		if name != tt.wantName {
			t.Errorf("SeasonName(%s, %d) = %q, want %q", tt.typ, tt.number, name, tt.wantName)
		}
		if got := describeName(ParseName(name)); got != tt.wantParsed {
			t.Errorf("ParseName(%q) = %q, want %q", name, got, tt.wantParsed)
		}
	}
}

func TestSearchVector(t *testing.T) {
	tests := []struct {
		name                 string
		typ                  SeriesType
		title, originalTitle string
		number               int
		seasonTitle          string
		want                 string
	}{
		{
			"剧名和季号为 A 档，原名为 B 档，季标题为 C 档，季号在最后", TypeTV, "星海旅人", "Star Voyager", 2, "归航",
			"'星':1A '星海':1A '海':2A '海旅':2A '旅':3A '旅人':3A '人':4A " +
				"'star':6B 'voyager':11B '归':19C '归航':19C '航':20C '2':22A",
		},
		{"特别篇没有季号", TypeTV, "星海", "", 0, "", "'星':1A '星海':1A '海':2A"},
		{
			"电影没有季号", TypeMovie, "长夜灯塔", "", 1, "",
			"'长':1A '长夜':1A '夜':2A '夜灯':2A '灯':3A '灯塔':3A '塔':4A",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SearchVector(tt.typ, tt.title, tt.originalTitle, tt.number, tt.seasonTitle); got != tt.want {
				t.Errorf("SearchVector() = %q\nwant %q", got, tt.want)
			}
		})
	}
}
