package source

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// named 一个还没认集号的条目，ref 与名称相同。
func named(label string) CollectionItem {
	return CollectionItem{Ref: Ref(`"` + label + `"`), Label: label}
}

func TestParseEpisodeRule(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string
		wantErr  string
	}{
		{name: "带捕获组的正则", patterns: []string{`第(\d+)集`, `EP(?P<episode>\d+)`}},
		{name: "空列表", patterns: []string{}, wantErr: "至少要有一条集号规则"},
		{name: "太多", patterns: slices.Repeat([]string{`(\d+)`}, 11), wantErr: "集号规则最多 10 条"},
		{name: "空的", patterns: []string{`(\d+)`, ""}, wantErr: "第 2 条集号规则是空的"},
		{name: "不是合法的正则", patterns: []string{`第(\d+集`}, wantErr: "第 1 条集号规则不是合法的正则：missing closing )"},
		{name: "没有捕获组", patterns: []string{`第\d+集`}, wantErr: `第 1 条集号规则里要有一个捕获组，例如 第(\d+)集`},
		{name: "太长", patterns: []string{"(" + strings.Repeat("a", 199) + ")"}, wantErr: "第 1 条集号规则不能超过 200 个字符"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := ParseEpisodeRule(tt.patterns)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ParseEpisodeRule(%q) error = %v, want %q", tt.patterns, err, tt.wantErr)
				}
				return
			}
			if err != nil || !slices.Equal(r.Patterns(), tt.patterns) {
				t.Fatalf("ParseEpisodeRule(%q) = (%q, %v), want (%q, nil)", tt.patterns, r.Patterns(), err, tt.patterns)
			}
		})
	}
}

func TestNumberItems(t *testing.T) {
	tests := []struct {
		name     string
		patterns []string // nil 为默认规则
		col      Collection
		want     []CollectionItem
	}{
		{
			name: "不按规则编号的合集：序号照旧，只整理",
			col: Collection{Items: []CollectionItem{
				numbered("第1话", 1), numbered("第1话 另一个", 1), unmatched("SP", "集号「SP」不是整数"),
			}},
			want: []CollectionItem{
				unmatched("第1话", "集号重复"), unmatched("第1话 另一个", "集号重复"), unmatched("SP", "集号「SP」不是整数"),
			},
		},
		{
			name: "默认规则：认写明的集号，不认没有标注的数字",
			col: Collection{NumberedByRule: true, Items: []CollectionItem{
				named("正式版【某番】第1集 中文字幕 / 01"), named("某番 EP02"), named("某番 S01E03"), named("某番 ＥＰ０４"),
				named("某番 PV"), named("【合辑】全12集 周更 / 05"), named("12"),
			}},
			want: []CollectionItem{
				numbered("正式版【某番】第1集 中文字幕 / 01", 1), numbered("某番 EP02", 2), numbered("某番 S01E03", 3),
				numbered("某番 ＥＰ０４", 4), unmatched("某番 PV", "不符合集号规则"),
				unmatched("【合辑】全12集 周更 / 05", "不符合集号规则"), unmatched("12", "不符合集号规则"),
			},
		},
		{
			name: "默认规则：按优先级，S01E03 先于第N集",
			col:  Collection{NumberedByRule: true, Items: []CollectionItem{named("某番 第1集 S01E03")}},
			want: []CollectionItem{numbered("某番 第1集 S01E03", 3)},
		},
		{
			name:     "正则：第一个捕获组为集号",
			patterns: []string{`第(\d+)话|EP(\d+)`},
			col: Collection{NumberedByRule: true, Items: []CollectionItem{
				named("某番（中字）第03话"), named("某番 EP4"), named("某番 PV"),
			}},
			want: []CollectionItem{
				numbered("某番（中字）第03话", 3),
				unmatched("某番 EP4", "不符合集号规则"), unmatched("某番 PV", "不符合集号规则"),
			},
		},
		{
			name:     "正则：捕获到的清洗为 NFKC 之后不是整数时对不上，不再试后面的",
			patterns: []string{`第(.+?)话`, `(\d+)`},
			col: Collection{NumberedByRule: true, Items: []CollectionItem{
				named("第１２话"), named("第一话"), named("第99999999999话"), named("第-1话"),
			}},
			want: []CollectionItem{
				numbered("第１２话", 12),
				unmatched("第一话", "集号「一」不是整数"), unmatched("第99999999999话", "集号「99999999999」不是整数"),
				unmatched("第-1话", "集号「-1」不是整数"),
			},
		},
		{
			name:     "正则：有 episode 组时取它；先按原文匹配，匹配不上再按清洗为 NFKC 的标签匹配",
			patterns: []string{`S(\d+)E(?P<episode>\d+)`, `（第(\d+)话）`, `EP(\d+)`},
			col: Collection{NumberedByRule: true, Items: []CollectionItem{
				named("某番 S02E07"), named("某番（第3话）"), named("某番 ＥＰ０５"),
			}},
			want: []CollectionItem{numbered("某番 S02E07", 7), numbered("某番（第3话）", 3), numbered("某番 ＥＰ０５", 5)},
		},
		{
			name:     "认出的集号重复时两条都对不上",
			patterns: []string{`(\d+)`},
			col:      Collection{NumberedByRule: true, Items: []CollectionItem{named("1 上"), named("1 下"), named("2")}},
			want:     []CollectionItem{unmatched("1 上", "集号重复"), unmatched("1 下", "集号重复"), numbered("2", 2)},
		},
		{
			name: "按规则编号时适配器给的序号不算数",
			col:  Collection{NumberedByRule: true, Items: []CollectionItem{numbered("第2集", 7)}},
			want: []CollectionItem{numbered("第2集", 2)},
		},
		{name: "空列表", col: Collection{NumberedByRule: true}, want: []CollectionItem{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := DefaultEpisodeRule()
			if tt.patterns != nil {
				var err error
				if r, err = ParseEpisodeRule(tt.patterns); err != nil {
					t.Fatal(err)
				}
			}
			in := append([]CollectionItem(nil), tt.col.Items...)
			got := NumberItems(tt.col, r)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NumberItems() = %+v\nwant %+v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.col.Items, in) && len(in) > 0 {
				t.Errorf("改动了传入的切片：%+v", tt.col.Items)
			}
		})
	}
}
