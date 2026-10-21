package source

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// EpisodeRuleConfig is a saved snapshot; defaults never reinterpret existing bindings.
type EpisodeRuleConfig struct {
	Version  int              `json:"version"`
	Scope    string           `json:"scope"`
	Rules    []EpisodePattern `json:"rules"`
	Excludes []EpisodeExclude `json:"excludes"`
}

type EpisodePattern struct {
	Pattern    string `json:"pattern"`
	Adjustment int64  `json:"adjustment"`
}

type EpisodeExclude struct {
	Pattern string `json:"pattern"`
	Kind    string `json:"kind"`
}

const (
	ReasonNoMatch        = "noMatch"
	ReasonNonRegular     = "nonRegular"
	ReasonCustomExcluded = "customExcluded"
	ReasonInvalidNumber  = "invalidNumber"
	ReasonAdjustment     = "adjustmentOutOfRange"
	ReasonMapping        = "mappingOutOfRange"
	ReasonDuplicate      = "duplicateNumber"
	ReasonMissingTitle   = "missingLabelParts"
	// MaxEpisodeNumber matches the catalog's PostgreSQL integer column.
	MaxEpisodeNumber = math.MaxInt32
)

// EpisodeTrace contains recognition evidence only, never a source ref.
type EpisodeTrace struct {
	Version        int               `json:"version"`
	Scope          string            `json:"scope"`
	ScopeLabel     string            `json:"scopeLabel"`
	Input          string            `json:"input"`
	Normalized     bool              `json:"normalized"`
	RuleIndex      *int              `json:"ruleIndex,omitempty"` // one based
	Captured       string            `json:"captured,omitempty"`
	Season         string            `json:"season,omitempty"`
	RawNumber      *int64            `json:"rawNumber,omitempty"`
	Adjustment     int64             `json:"adjustment"`
	AdjustedNumber *int64            `json:"adjustedNumber,omitempty"`
	LocalNumber    *int64            `json:"localNumber,omitempty"`
	ExcludeIndex   *int              `json:"excludeIndex,omitempty"`
	ExcludePattern string            `json:"excludePattern,omitempty"`
	Conflicts      []EpisodeConflict `json:"conflicts,omitempty"`
}

type EpisodeConflict struct {
	Row   int    `json:"row"`
	Label string `json:"label"`
}

func (r EpisodeRule) Config() EpisodeRuleConfig {
	c := r.config
	c.Rules, c.Excludes = slices.Clone(c.Rules), slices.Clone(c.Excludes)
	return c
}

func ParseEpisodeRuleConfig(c EpisodeRuleConfig) (EpisodeRule, error) {
	if c.Version != 1 && c.Version != 2 {
		return EpisodeRule{}, errors.New("集号规则版本不合法")
	}
	if c.Scope != "full" && c.Scope != "entry" {
		return EpisodeRule{}, errors.New("匹配范围必须是 full 或 entry")
	}
	if len(c.Excludes) > maxPatterns {
		return EpisodeRule{}, errors.New("排除规则最多 10 条")
	}
	patterns := make([]string, len(c.Rules))
	for i, p := range c.Rules {
		if p.Adjustment < -MaxEpisodeNumber || p.Adjustment > MaxEpisodeNumber {
			return EpisodeRule{}, fmt.Errorf("第 %d 条规则的集号加减值越界", i+1)
		}
		if c.Version == 1 && p.Adjustment != 0 {
			return EpisodeRule{}, errors.New("旧版规则不支持集号加减")
		}
		patterns[i] = p.Pattern
	}
	if c.Version == 1 && (c.Scope != "full" || len(c.Excludes) != 0) {
		return EpisodeRule{}, errors.New("旧版规则只支持完整标题且不能排除条目")
	}
	r, err := ParseEpisodeRule(patterns)
	if err != nil {
		return EpisodeRule{}, err
	}
	r.config.Version, r.config.Scope = c.Version, c.Scope
	r.config.Excludes = make([]EpisodeExclude, len(c.Excludes))
	for i, p := range c.Rules {
		r.patterns[i].adjustment = p.Adjustment
		r.config.Rules[i].Adjustment = p.Adjustment
	}
	for i, e := range c.Excludes {
		e.Pattern = strings.TrimSpace(e.Pattern)
		if e.Kind != "nonRegular" && e.Kind != "custom" {
			return EpisodeRule{}, fmt.Errorf("第 %d 条排除规则种类不合法", i+1)
		}
		if e.Pattern == "" || utf8.RuneCountInString(e.Pattern) > maxPatternLength {
			return EpisodeRule{}, fmt.Errorf("第 %d 条排除规则必须为 1～200 个字符", i+1)
		}
		re, err := regexp.Compile(e.Pattern)
		if err != nil {
			return EpisodeRule{}, fmt.Errorf("第 %d 条排除规则不是合法的正则", i+1)
		}
		r.excludes = append(r.excludes, re)
		r.config.Excludes[i] = e
	}
	return r, nil
}

func (r EpisodeRule) matchV2(it CollectionItem) CollectionItem {
	t := &EpisodeTrace{Version: 2, Scope: r.config.Scope}
	it.Trace = t
	fail := func(code, reason string) CollectionItem {
		it.Number, it.Unmatched, it.ReasonCode = 0, reason, code
		return it
	}
	if len(it.LabelParts) < 1 || len(it.LabelParts) > 2 {
		return fail(ReasonMissingTitle, "标题信息不完整，请重新预览合集")
	}
	leaf := norm.NFKC.String(it.LabelParts[len(it.LabelParts)-1])
	t.Input, t.ScopeLabel = it.Label, "完整标题"
	if r.config.Scope == "entry" {
		t.Input, t.ScopeLabel = it.LabelParts[len(it.LabelParts)-1], "条目标题"
	}
	text := norm.NFKC.String(t.Input)
	t.Normalized = text != t.Input || leaf != it.LabelParts[len(it.LabelParts)-1]
	for i, re := range r.excludes {
		if re.MatchString(leaf) {
			t.ExcludeIndex, t.ExcludePattern = new(i+1), r.config.Excludes[i].Pattern
			if r.config.Excludes[i].Kind == "nonRegular" {
				return fail(ReasonNonRegular, "仅正片：已排除特别篇或非正片")
			}
			return fail(ReasonCustomExcluded, "已被自定义排除规则排除")
		}
	}
	match, ok := r.findLast(text)
	if !ok {
		return fail(ReasonNoMatch, notMatchPattern)
	}
	t.RuleIndex, t.Captured, t.Season = new(match.rule+1), match.captured, match.season
	t.Adjustment = r.patterns[match.rule].adjustment
	n, err := strconv.ParseUint(match.captured, 10, 31)
	if err != nil {
		return fail(ReasonInvalidNumber, "集号必须是不小于 0 且不超过 2147483647 的整数："+match.captured)
	}
	raw := int64(n)
	adjusted := raw + t.Adjustment
	t.RawNumber, t.AdjustedNumber = &raw, &adjusted
	if adjusted < 0 || adjusted > MaxEpisodeNumber {
		return fail(ReasonAdjustment, "集号加减后的结果越界")
	}
	it.Number, it.Unmatched, it.ReasonCode = int(adjusted), "", ""
	return it
}

// NumberEntries numbers structured labels without source deduplication (folder entries).
func NumberEntries(items []CollectionItem, r EpisodeRule) []CollectionItem {
	out := make([]CollectionItem, len(items))
	for i, it := range items {
		out[i] = r.match(it)
	}
	return markDuplicateNumbers(out)
}
