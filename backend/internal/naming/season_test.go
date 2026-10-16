package naming

import "testing"

// TestSeasonName 季的名称，以及 Parse 能从它拆回剧名和季号：插件手动选过一季之后，拿这个名称作为关键词搜回同一季。
// 第 1 季和电影的名称就是剧名，没有季号。
func TestSeasonName(t *testing.T) {
	tests := []struct {
		typ        SeriesType
		number     int
		wantName   string
		wantParsed string // Parse 拆出的"标题 | 季号 | 集号"
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
		if got := describeName(Parse(name)); got != tt.wantParsed {
			t.Errorf("Parse(%q) = %q, want %q", name, got, tt.wantParsed)
		}
	}
}
