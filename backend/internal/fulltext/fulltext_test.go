package fulltext

import (
	"strings"
	"testing"
)

func TestVector(t *testing.T) {
	a := func(text string) Field { return Field{Weight: WeightA, Text: text} }
	b := func(text string) Field { return Field{Weight: WeightB, Text: text} }
	c := func(text string) Field { return Field{Weight: WeightC, Text: text} }

	tests := []struct {
		name   string
		fields []Field
		want   string
	}{
		{
			"中日韩字符串产出单字和相邻两字",
			[]Field{a("星海旅人")},
			"'星':1A '星海':1A '海':2A '海旅':2A '旅':3A '旅人':3A '人':4A",
		},
		{
			"假名与长音符",
			[]Field{a("オーバー")},
			"'オ':1A 'オー':1A 'ー':2A 'ーバ':2A 'バ':3A 'バー':3A",
		},
		{"谚文", []Field{a("별빛")}, "'별':1A '별빛':1A '빛':2A"},
		{"字母与数字相接处切开", []Field{a("Overlord2")}, "'overlord':1A '2':9A"},
		{
			"其余字符都是分隔符",
			[]Field{a("Re:Zero - 从零开始")},
			"'re':1A 'zero':4A '从':11A '从零':11A '零':12A '零开':12A '开':13A '开始':13A '始':14A",
		},
		{"NFKC 与小写", []Field{a("ＳＴＡＲ　Ｖｏｙａｇｅｒ ２")}, "'star':1A 'voyager':6A '2':14A"},
		{
			"各字段的位置接着往后数",
			[]Field{a("星海 第2季"), b("Star Voyager"), c("归航")},
			"'星':1A '星海':1A '海':2A '第':4A '2':5A '季':6A 'star':8B 'voyager':13B '归':21C '归航':21C '航':22C",
		},
		{
			"同一个词只保留一次，取最高档",
			[]Field{a("星海"), b("星海 Star"), c("star")},
			"'星':1A '星海':1A '海':2A 'star':7B",
		},
		{"后出现的更高档覆盖位置", []Field{c("旅人"), a("旅人")}, "'旅':4A '旅人':4A '人':5A"},
		{"空字段", []Field{a(""), b("  ")}, ""},
		{
			"超过 PostgreSQL 长度上限的词丢掉",
			[]Field{a(strings.Repeat("a", maxLexemeBytes+1) + " " + strings.Repeat("b", maxLexemeBytes) + " 星")},
			"'" + strings.Repeat("b", maxLexemeBytes) + "':2050A '星':4098A",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Vector(tt.fields...); got != tt.want {
				t.Errorf("Vector() = %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestQuery(t *testing.T) {
	tests := []struct {
		name    string
		keyword string
		want    string
	}{
		{"两字以上的中日韩字符串只用相邻两字", "星海旅人", "'星海' & '海旅' & '旅人'"},
		{"单个字用单字", "星 海", "'星' & '海'"},
		{"剧名紧跟季号", "星海旅人2", "'星海' & '海旅' & '旅人' & '2'"},
		{"字母串整词", "Star Voyager", "'star' & 'voyager'"},
		{"NFKC 与小写", "ＯＶＥＲＬＯＲＤ２", "'overlord' & '2'"},
		{"重复的词只留一个", "哈哈哈", "'哈哈'"},
		{"自己输出的季名称", "星海旅人 第2季", "'星海' & '海旅' & '旅人' & '第' & '2' & '季'"},
		{"切不出词", " !?・ ", ""},
		{"有词超过长度上限时不会有结果", "星海 " + strings.Repeat("a", maxLexemeBytes+1), ""},
		{"长度上限之内", strings.Repeat("a", maxLexemeBytes), "'" + strings.Repeat("a", maxLexemeBytes) + "'"},
		{"空串", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Query(tt.keyword); got != tt.want {
				t.Errorf("Query(%q) = %q, want %q", tt.keyword, got, tt.want)
			}
		})
	}
}
