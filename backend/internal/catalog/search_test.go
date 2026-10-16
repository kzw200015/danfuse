package catalog

import (
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/naming"
)

func TestSearchVector(t *testing.T) {
	tests := []struct {
		name                 string
		typ                  naming.SeriesType
		title, originalTitle string
		number               int
		seasonTitle          string
		want                 string
	}{
		{
			"剧名和季号为 A 档，原名为 B 档，季标题为 C 档，季号在最后", naming.TypeTV, "星海旅人", "Star Voyager", 2, "归航",
			"'星':1A '星海':1A '海':2A '海旅':2A '旅':3A '旅人':3A '人':4A " +
				"'star':6B 'voyager':11B '归':19C '归航':19C '航':20C '2':22A",
		},
		{"特别篇没有季号", naming.TypeTV, "星海", "", 0, "", "'星':1A '星海':1A '海':2A"},
		{
			"电影没有季号", naming.TypeMovie, "长夜灯塔", "", 1, "",
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
