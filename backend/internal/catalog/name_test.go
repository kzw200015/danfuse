package catalog

import (
	"fmt"
	"testing"
)

// describeName 把解析结果写成一行便于比较："标题 | 季号 | 集号"，没有的写 -。
func describeName(p ParsedName) string {
	number := func(n *int) string {
		if n == nil {
			return "-"
		}
		return fmt.Sprint(*n)
	}
	return fmt.Sprintf("%s | %s | %s", p.Title, number(p.Season), number(p.Episode))
}

func TestParseName(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"尼古喵喵 S01E11", "尼古喵喵 | 1 | 11"},
		{"Night Watch s2e1", "Night Watch | 2 | 1"},
		{"星海旅人S02 E13", "星海旅人 | 2 | 13"},
		{"星海旅人 Ｓ０２Ｅ１３", "星海旅人 | 2 | 13"}, // 全角先清洗为半角
		{"星海旅人 S00E01", "星海旅人 | 0 | 1"},
		{"星海旅人 S02", "星海旅人 | 2 | -"},
		{"星海旅人 第2季", "星海旅人 | 2 | -"},
		{"星海旅人 第 12 季", "星海旅人 | 12 | -"},
		{"星海旅人 特别篇", "星海旅人 | 0 | -"},
		{"星海旅人 特別篇", "星海旅人 | 0 | -"},
		{"星海旅人 第13话", "星海旅人 | - | 13"},
		{"星海旅人 第 13 話", "星海旅人 | - | 13"},
		{"星海旅人 第13集", "星海旅人 | - | 13"},
		{"Night Watch EP01", "Night Watch | - | 1"},
		{"星海旅人 第2季 第13话", "星海旅人 | 2 | 13"},
		{"星海旅人 特别篇 第1话", "星海旅人 | 0 | 1"},
		{"星海旅人 S02E13 第1话", "星海旅人   第1话 | 2 | 13"},                  // 季号和集号各只取第一个
		{"Night.Watch.S02E01.1080p", "Night.Watch. .1080p | 2 | 1"}, // 其他内容不清理
		// 没有标注的数字不拆
		{"星海旅人2", "星海旅人2 | - | -"},
		{"Mob Psycho 100", "Mob Psycho 100 | - | -"},
		{"[字幕组] 星海旅人 - 01", "[字幕组] 星海旅人 - 01 | - | -"},
		{"Steps1e1", "Steps1e1 | - | -"}, // S 前面要隔开
		{"S01E11", " | 1 | 11"},
		{"", " | - | -"},
	}
	for _, tt := range tests {
		if got := describeName(ParseName(tt.name)); got != tt.want {
			t.Errorf("ParseName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}
