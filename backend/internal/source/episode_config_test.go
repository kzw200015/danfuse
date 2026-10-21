package source

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func entryTitle(parent, leaf string) CollectionItem {
	return CollectionItem{
		Ref: Ref(`"` + leaf + `"`), Label: parent + LabelSeparator + leaf,
		LabelParts: []string{parent, leaf},
	}
}

func mustConfig(t *testing.T, c EpisodeRuleConfig) EpisodeRule {
	t.Helper()
	r, err := ParseEpisodeRuleConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustTemplate(t *testing.T, id string, season *int, adjustment int64) EpisodePattern {
	t.Helper()
	p, err := RenderEpisodeTemplate(id, season, adjustment)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEpisodeV2ConversationTitles(t *testing.T) {
	tests := []struct {
		parent, leaf string
		number       int
		code         string
	}{
		{"【悠哉日常大王】1-2季 26话 超清中字", "第1话 转学生来了", 1, ""},
		{"【悠哉日常大王】1-2季 26话 超清中字", "第二季 第1话 上一年级了", 1, ""},
		{"【悠哉日常大王】1-2季 26话 超清中字", "第13话 OAD 要去冲绳了", 0, ReasonNonRegular},
		{"【彻夜之歌 全两季】全25集 超清中字（未删减版）", "第一季01", 1, ""},
		{"【吸特乐】全13话 超清中字 未删减版（更至第2话）", "1 洗眼睛", 1, ""},
		{"【吸特乐】全13话 超清中字 未删减版（更至第2话）", "2 洗眼睛", 2, ""},
		{"【我心里危险的东西 全两季】全25集+特别篇 超清中字（未删减版）", "第二季09", 9, ""},
		{"更至第2话", "第３话 标题", 3, ""},
		{"第2话", "未知标题", 0, ReasonNoMatch},
		{"全25集+特别篇", "01", 1, ""},
		{"某番", "EP03", 3, ""},
		{"某番", "Ｓ０１Ｅ０３", 3, ""},
		{"某番", "1080P", 0, ReasonNoMatch},
		{"某番", "1.5", 0, ReasonNoMatch},
		{"某番", "1-2季", 0, ReasonNoMatch},
		{"某番", "第一季01.5", 0, ReasonNoMatch},
		{"某番", "EP01-02", 0, ReasonNoMatch},
		{"某番", "1 - 2", 0, ReasonNoMatch},
		{"某番", "1 ～ 2", 0, ReasonNoMatch},
		{"某番", "EP01 EP03", 3, ""},
		{"某番", "S01E01 S01E03", 3, ""},
		{"某番", "特别篇", 0, ReasonNonRegular},
		{"某番", "1 SPY", 1, ""},
		{"某番", "SPY 1", 0, ReasonNoMatch},
	}
	for _, tt := range tests {
		t.Run(tt.leaf, func(t *testing.T) {
			it := NumberEntries([]CollectionItem{entryTitle(tt.parent, tt.leaf)}, DefaultEpisodeRuleV2())[0]
			if it.Number != tt.number || it.ReasonCode != tt.code {
				t.Fatalf("number=%d code=%s reason=%s trace=%+v", it.Number, it.ReasonCode, it.Unmatched, it.Trace)
			}
			if it.Trace == nil || it.Trace.Input != tt.leaf || it.Trace.Scope != "entry" {
				t.Fatalf("missing trace: %+v", it.Trace)
			}
		})
	}
}

func TestEpisodeV2SeasonTemplatesAndPriority(t *testing.T) {
	c := DefaultEpisodeRuleV2().Config()
	c.Rules = []EpisodePattern{
		mustTemplate(t, "seasonNumber", new(1), 0),
		mustTemplate(t, "seasonNumber", new(2), 12),
	}
	items := []CollectionItem{entryTitle("全25集+特别篇", "第一季01"), entryTitle("全25集+特别篇", "第二季01"), entryTitle("全25集+特别篇", "第二季09")}
	got := NumberEntries(items, mustConfig(t, c))
	for i, want := range []int{1, 13, 21} {
		if got[i].Number != want || got[i].Unmatched != "" {
			t.Fatalf("item %d = %+v, want %d", i, got[i], want)
		}
	}
	if got[2].Trace.Season != "第二季" || got[2].Trace.Captured != "09" || *got[2].Trace.RawNumber != 9 {
		t.Fatalf("trace = %+v", got[2].Trace)
	}
	// A specific rule inserted before general defaults must win ties, independent of its adjustment.
	c = DefaultEpisodeRuleV2().Config()
	c.Rules = append([]EpisodePattern{mustTemplate(t, "seasonNumber", new(2), 12)}, c.Rules...)
	it := NumberEntries(items[2:], mustConfig(t, c))[0]
	if it.Number != 21 || *it.Trace.RuleIndex != 1 {
		t.Fatalf("override = %+v / %+v", it, it.Trace)
	}
	// Unmarked entries are selected explicitly, never assumed to mean season one.
	c.Rules = []EpisodePattern{mustTemplate(t, "episodeLabel", nil, 0)}
	got = NumberEntries([]CollectionItem{entryTitle("某番", "第1话"), entryTitle("某番", "第二季 第1话")}, mustConfig(t, c))
	if got[0].Number != 1 || got[1].ReasonCode != ReasonNoMatch {
		t.Fatalf("unmarked season selection = %+v", got)
	}
	for _, id := range []string{"seasonNumber", "seasonEpisodeLabel", "sxe"} {
		c.Rules = []EpisodePattern{mustTemplate(t, id, new(2), 0)}
		var labels []string
		switch id {
		case "seasonNumber":
			labels = []string{"第二季09", "第2季09", "第一季09"}
		case "seasonEpisodeLabel":
			labels = []string{"第二季 第9话", "第2季 第9话", "第一季 第9话"}
		default:
			labels = []string{"S02E09", "s2e09", "S01E09"}
		}
		for i, label := range labels {
			it := NumberEntries([]CollectionItem{entryTitle("某番", label)}, mustConfig(t, c))[0]
			if (i < 2 && it.Number != 9) || (i == 2 && it.ReasonCode != ReasonNoMatch) {
				t.Fatalf("%s: %+v", label, it)
			}
		}
	}
}

func TestEpisodeV2Exclusions(t *testing.T) {
	for _, marker := range []string{"OAD", "oad01", "OVA", "SP 1", "PV", "OP", "ED", "【ＳＰ０１】", "预告", "特别篇"} {
		it := NumberEntries([]CollectionItem{entryTitle("标题", "第13话 "+marker)}, DefaultEpisodeRuleV2())[0]
		if it.ReasonCode != ReasonNonRegular || it.Trace.ExcludeIndex == nil {
			t.Errorf("%s: %+v", marker, it)
		}
	}
	for _, marker := range []string{"SPY", "BSP01", "OADventure", "SPECIAL", "OPUS", "EDISON"} {
		it := NumberEntries([]CollectionItem{entryTitle("标题", "1 "+marker)}, DefaultEpisodeRuleV2())[0]
		if it.Number != 1 || it.Unmatched != "" {
			t.Errorf("false exclusion %s: %+v", marker, it)
		}
	}
	c := DefaultEpisodeRuleV2().Config()
	c.Excludes = nil
	if it := NumberEntries([]CollectionItem{entryTitle("标题", "第13话 OAD")}, mustConfig(t, c))[0]; it.Number != 13 {
		t.Fatal(it)
	}
	c.Scope = "full"
	c.Rules = []EpisodePattern{{Pattern: `第(\d+)话`}}
	c.Excludes = []EpisodeExclude{{Kind: "custom", Pattern: "预览版"}}
	r := mustConfig(t, c)
	got := NumberEntries([]CollectionItem{entryTitle("预览版 第1话", "正片"), entryTitle("第1话", "预览版")}, r)
	if got[0].Number != 1 || got[1].ReasonCode != ReasonCustomExcluded {
		t.Fatal(got)
	}
}

func TestEpisodeV2DuplicatesAndBounds(t *testing.T) {
	c := DefaultEpisodeRuleV2().Config()
	c.Rules = []EpisodePattern{
		mustTemplate(t, "seasonNumber", new(1), 0),
		mustTemplate(t, "seasonNumber", new(2), -1),
	}
	input := []CollectionItem{entryTitle("合集", "第一季01"), entryTitle("合集", "第二季02")}
	got := NumberEntries(input, mustConfig(t, c))
	for _, it := range got {
		if it.Number != 0 || it.ReasonCode != ReasonDuplicate || *it.Trace.AdjustedNumber != 1 || len(it.Trace.Conflicts) != 2 {
			t.Fatal(it)
		}
	}
	if input[0].Trace != nil || input[0].Number != 0 {
		t.Fatal("mutated input")
	}
	c.Rules = []EpisodePattern{{Pattern: `(\d+)`, Adjustment: -2}}
	if got := NumberEntries([]CollectionItem{entryTitle("", "1")}, mustConfig(t, c))[0]; got.ReasonCode != ReasonAdjustment {
		t.Fatal(got)
	}
	c.Rules[0].Adjustment = MaxEpisodeNumber
	for _, text := range []string{"1", "99999999999999999999999"} {
		got := NumberEntries([]CollectionItem{entryTitle("", text)}, mustConfig(t, c))[0]
		if got.ReasonCode != ReasonAdjustment && got.ReasonCode != ReasonInvalidNumber {
			t.Fatal(got)
		}
	}
	c = DefaultEpisodeRuleV2().Config()
	item := entryTitle("标题", "第1话")
	got = NumberItems(Collection{NumberedByRule: true, Items: []CollectionItem{item, item}}, mustConfig(t, c))
	if len(got) != 1 || got[0].Number != 1 {
		t.Fatal(got)
	}
	if got := NumberEntries([]CollectionItem{{Label: "合集 / 第1话"}}, mustConfig(t, c))[0]; got.ReasonCode != ReasonMissingTitle {
		t.Fatal(got)
	}
}

func TestEpisodeV1NormalizationPreserved(t *testing.T) {
	it := entryTitle("更至第2话", "第３话 标题")
	legacy := NumberEntries([]CollectionItem{it}, DefaultEpisodeRule())[0]
	modern := NumberEntries([]CollectionItem{it}, DefaultEpisodeRuleV2())[0]
	if legacy.Number != 2 || modern.Number != 3 || !modern.Trace.Normalized {
		t.Fatalf("legacy=%+v modern=%+v", legacy, modern)
	}
	c := DefaultEpisodeRuleV2().Config()
	c.Scope = "full"
	c.Rules = []EpisodePattern{{Pattern: `第(\d+)话`}}
	if got := NumberEntries([]CollectionItem{it}, mustConfig(t, c))[0]; got.Number != 3 {
		t.Fatal(got)
	}
}

func TestEpisodeConfigValidationAndSnapshots(t *testing.T) {
	good := DefaultEpisodeRuleV2().Config()
	tests := []struct {
		name string
		edit func(*EpisodeRuleConfig)
	}{
		{"version", func(c *EpisodeRuleConfig) { c.Version = 3 }},
		{"scope", func(c *EpisodeRuleConfig) { c.Scope = "unknown" }},
		{"no rules", func(c *EpisodeRuleConfig) { c.Rules = nil }},
		{"too many", func(c *EpisodeRuleConfig) { c.Rules = slices.Repeat(c.Rules, 2) }},
		{"capture", func(c *EpisodeRuleConfig) { c.Rules[0].Pattern = `\d+` }},
		{"length", func(c *EpisodeRuleConfig) { c.Rules[0].Pattern = "(" + strings.Repeat("中", 199) + ")" }},
		{"adjustment", func(c *EpisodeRuleConfig) { c.Rules[0].Adjustment = MaxEpisodeNumber + 1 }},
		{"exclude kind", func(c *EpisodeRuleConfig) { c.Excludes[0].Kind = "unknown" }},
		{"exclude regex", func(c *EpisodeRuleConfig) { c.Excludes[0].Pattern = "[" }},
		{"exclude length", func(c *EpisodeRuleConfig) { c.Excludes[0].Pattern = strings.Repeat("中", 201) }},
		{"exclude count", func(c *EpisodeRuleConfig) { c.Excludes = slices.Repeat(c.Excludes, 11) }},
		{"v1 scope", func(c *EpisodeRuleConfig) { c.Version = 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DefaultEpisodeRuleV2().Config()
			tt.edit(&c)
			if _, err := ParseEpisodeRuleConfig(c); err == nil {
				t.Fatal("accepted invalid config")
			}
		})
	}
	r := mustConfig(t, good)
	snapshot := r.Config()
	snapshot.Rules[0].Pattern = "changed"
	snapshot.Excludes[0].Pattern = "changed"
	if !reflect.DeepEqual(r.Config(), good) {
		t.Fatal("config does not own its snapshot")
	}
	for _, id := range []string{"episodeLabel", "numberPrefix", "numberOnly", "ep", "unknown"} {
		if _, err := RenderEpisodeTemplate(id, new(2), 0); err == nil {
			t.Fatalf("%s accepted unsupported season", id)
		}
	}
	for _, n := range []int{0, -1, 10000} {
		if _, err := RenderEpisodeTemplate("seasonNumber", &n, 0); err == nil {
			t.Fatal("accepted invalid season", n)
		}
	}
}
