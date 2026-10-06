package source

import (
	"reflect"
	"strings"
	"testing"
)

// named 一个还没认集号的条目，ref 与名称相同。
func named(label string) CollectionItem {
	return CollectionItem{Ref: Ref(`"` + label + `"`), Label: label}
}

func TestParseEpisodeRule(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr string
	}{
		{name: "空的是内置规则", pattern: ""},
		{name: "带捕获组的正则", pattern: `第(\d+)集`},
		{name: "不是合法的正则", pattern: `第(\d+集`, wantErr: "集号规则不是合法的正则：missing closing )"},
		{name: "没有捕获组", pattern: `第\d+集`, wantErr: `集号规则里要有一个捕获组，例如 第(\d+)集`},
		{name: "太长", pattern: "(" + strings.Repeat("a", 199) + ")", wantErr: "集号规则不能超过 200 个字符"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := ParseEpisodeRule(tt.pattern)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ParseEpisodeRule(%q) error = %v, want %q", tt.pattern, err, tt.wantErr)
				}
				return
			}
			if err != nil || r.Pattern() != tt.pattern {
				t.Fatalf("ParseEpisodeRule(%q) = (%q, %v), want (%q, nil)", tt.pattern, r.Pattern(), err, tt.pattern)
			}
		})
	}
}

func TestNumberItems(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		col     Collection
		want    []CollectionItem
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
			name: "内置规则：认写明的集号",
			col: Collection{NumberedByRule: true, Items: []CollectionItem{
				named("正式版【某番】第1集 中文字幕 / 01"), named("某番 EP02"), named("某番 S01E03"), named("某番 PV"),
			}},
			want: []CollectionItem{
				numbered("正式版【某番】第1集 中文字幕 / 01", 1), numbered("某番 EP02", 2), numbered("某番 S01E03", 3),
				unmatched("某番 PV", "名称里认不出集号"),
			},
		},
		{
			name: "内置规则：合集里有写明集号的条目时，不认末尾的数字",
			col: Collection{NumberedByRule: true, Items: []CollectionItem{
				named("【合辑】全12集 周更 / 01"), named("【合辑】全12集 周更 / 周更"), named("正式版 第1集 / 01"),
			}},
			want: []CollectionItem{
				unmatched("【合辑】全12集 周更 / 01", "名称里认不出集号"),
				unmatched("【合辑】全12集 周更 / 周更", "名称里认不出集号"),
				numbered("正式版 第1集 / 01", 1),
			},
		},
		{
			name: "内置规则：整个合集都没有写明集号时认末尾的数字",
			col: Collection{NumberedByRule: true, Items: []CollectionItem{
				named("某番 / 01"), named("某番 / ０２"), named("某番 / 周更"), named("12"), named("某番 / v2版"),
			}},
			want: []CollectionItem{
				numbered("某番 / 01", 1), numbered("某番 / ０２", 2), unmatched("某番 / 周更", "名称里认不出集号"),
				numbered("12", 12), unmatched("某番 / v2版", "名称里认不出集号"),
			},
		},
		{
			name:    "正则：第一个捕获组为集号",
			pattern: `第(\d+)话|EP(\d+)`,
			col: Collection{NumberedByRule: true, Items: []CollectionItem{
				named("某番（中字）第03话"), named("某番 EP4"), named("某番 PV"),
			}},
			want: []CollectionItem{
				numbered("某番（中字）第03话", 3),
				unmatched("某番 EP4", "不符合集号规则"), unmatched("某番 PV", "不符合集号规则"),
			},
		},
		{
			name:    "正则：捕获到的清洗为 NFKC 之后不是整数时对不上",
			pattern: `第(.+?)话`,
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
			name:    "认出的集号重复时两条都对不上",
			pattern: `(\d+)`,
			col:     Collection{NumberedByRule: true, Items: []CollectionItem{named("1 上"), named("1 下"), named("2")}},
			want:    []CollectionItem{unmatched("1 上", "集号重复"), unmatched("1 下", "集号重复"), numbered("2", 2)},
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
			r, err := ParseEpisodeRule(tt.pattern)
			if err != nil {
				t.Fatal(err)
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
