package source

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/kzw200015/danfuse/backend/internal/naming"
)

const (
	maxPatterns      = 10  // 集号规则最多的条数
	maxPatternLength = 200 // 一条正则最多的字符数
)

// notMatchPattern 每一条正则都匹配不上时对不上的原因。
const notMatchPattern = "不符合集号规则"

// EpisodeRule 集号规则：季绑定从条目名称（标签）认出合集序号的规则，只用于按规则编号的合集（Collection.NumberedByRule）。
// 是一组正则：每一条都在条目的标签里找，取集号结束得最靠后的那一处，一样靠后时取排在前面的正则（见 match）；
// 有名为 episode 的捕获组时取它，否则取第一个捕获组。
// 由 ParseEpisodeRule 或 DefaultEpisodeRule 得到，零值不可用。
type EpisodeRule struct {
	patterns []episodePattern
	config   EpisodeRuleConfig
	excludes []*regexp.Regexp
}

type episodePattern struct {
	re         *regexp.Regexp
	group      int // 集号所在捕获组的下标
	adjustment int64
}

// lastLevelEpisode 标签结尾、紧跟 LabelSeparator 的数字：下级标题只写了集号（"某番 / 05"）。
const lastLevelEpisode = `/ (?P<episode>\d{1,4})$`

// defaultRule 默认规则：naming.EpisodePatterns（与搜索、match 认集号的写法相同），再加上 lastLevelEpisode。
var defaultRule = func() EpisodeRule {
	r, err := ParseEpisodeRule(append(naming.EpisodePatterns(), lastLevelEpisode))
	if err != nil {
		panic(err)
	}
	return r
}()

// DefaultEpisodeRule 默认规则。季绑定保存的是创建时的副本，之后默认规则变了也不影响已有的季绑定。
func DefaultEpisodeRule() EpisodeRule {
	return defaultRule
}

// ParseEpisodeRule 解析一组正则（排在前面的优先）：每条先去掉前后的空白，至少 1 条、至多 10 条，每条是 RE2 正则，
// 要有捕获组、不超过 200 个字符。错误的内容是给用户看的提示。
func ParseEpisodeRule(patterns []string) (EpisodeRule, error) {
	switch {
	case len(patterns) == 0:
		return EpisodeRule{}, errors.New("至少要有一条集号规则")
	case len(patterns) > maxPatterns:
		return EpisodeRule{}, fmt.Errorf("集号规则最多 %d 条", maxPatterns)
	}
	r := EpisodeRule{patterns: make([]episodePattern, len(patterns))}
	for i, pattern := range patterns {
		p, err := parsePattern(strings.TrimSpace(pattern))
		if err != nil {
			return EpisodeRule{}, fmt.Errorf("第 %d 条集号规则%w", i+1, err)
		}
		r.patterns[i] = p
	}
	r.config = EpisodeRuleConfig{Version: 1, Scope: "full", Rules: make([]EpisodePattern, len(patterns)), Excludes: []EpisodeExclude{}}
	for i, p := range r.patterns {
		r.config.Rules[i] = EpisodePattern{Pattern: p.re.String()}
	}
	return r, nil
}

// parsePattern 解析一条正则，错误的内容接在"第 N 条集号规则"之后。
func parsePattern(pattern string) (episodePattern, error) {
	if pattern == "" {
		return episodePattern{}, errors.New("是空的")
	}
	if utf8.RuneCountInString(pattern) > maxPatternLength {
		return episodePattern{}, fmt.Errorf("不能超过 %d 个字符", maxPatternLength)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		if syntaxErr, ok := errors.AsType[*syntax.Error](err); ok {
			return episodePattern{}, fmt.Errorf("不是合法的正则：%s", syntaxErr.Code)
		}
		return episodePattern{}, errors.New("不是合法的正则")
	}
	if re.NumSubexp() == 0 {
		return episodePattern{}, errors.New(`里要有一个捕获组，例如 第(\d+)集`)
	}
	group := 1
	if i := re.SubexpIndex("episode"); i > 0 {
		group = i
	}
	return episodePattern{re: re, group: group}, nil
}

// Patterns 保存用的各条正则。
func (r EpisodeRule) Patterns() []string {
	patterns := make([]string, len(r.patterns))
	for i, p := range r.patterns {
		patterns[i] = p.re.String()
	}
	return patterns
}

// NumberItems 预览、保存之前整理 ListCollection 列出的条目：先用 UniqueRefs 去掉重复的 ref，
// 按规则编号的合集再用集号规则从标签认出各条目的序号（认不出的对不上），最后标出重复的序号。不改动传入的切片。
func NumberItems(c Collection, r EpisodeRule) []CollectionItem {
	items := UniqueRefs(c.Items)
	if c.NumberedByRule {
		for i, it := range items {
			items[i] = r.match(it)
		}
	}
	return markDuplicateNumbers(items)
}

// NumberLabels 用集号规则从一组名称认出序号，与按规则编号的合集同一套匹配和"集号重复"的标注，但不按 ref 去重：
// 结果与 labels 一一对应（名称相同的也各自保留），填名称、集号与识别诊断，不填 Ref。
func NumberLabels(labels []string, r EpisodeRule) []CollectionItem {
	items := make([]CollectionItem, len(labels))
	for i, label := range labels {
		it := CollectionItem{Label: label}
		if r.config.Version == 2 {
			it.LabelParts = []string{label}
		}
		items[i] = r.match(it)
	}
	return markDuplicateNumbers(items)
}

// match 认一个条目的序号：每一条正则都在标签里找，取集号（捕获组）结束得最靠后的那一处，一样靠后时取排在前面的正则。
// 标签越靠后越具体（见 CollectionItem.Label），两级都写了集号时以下级的为准。先按标签原文匹配（用户照着看到的标签写），
// 每一条都匹配不上、且清洗为 NFKC 后有变化时，再按清洗后的标签匹配（全角的数字、字母也能认出）；集号的捕获组没有参与匹配时
// 算这一处匹配不上。取到的（清洗为 NFKC 之后）不是不小于 0 的整数时对不上，原因写明捕获到的内容，不退而取别的匹配。
func (r EpisodeRule) match(it CollectionItem) CollectionItem {
	if r.config.Version == 2 {
		return r.matchV2(it)
	}
	t := &EpisodeTrace{Version: 1, Scope: "full", ScopeLabel: "完整标题（旧版）", Input: it.Label}
	it.Trace, it.ReasonCode = t, ""
	match, ok := r.findLast(it.Label)
	if normalized := norm.NFKC.String(it.Label); !ok && normalized != it.Label {
		t.Normalized = true
		match, ok = r.findLast(normalized)
	}
	if !ok {
		it.Number, it.Unmatched, it.ReasonCode = 0, notMatchPattern, ReasonNoMatch
		return it
	}
	captured := match.captured
	t.RuleIndex, t.Captured, t.Season = new(match.rule+1), captured, match.season
	t.Normalized = t.Normalized || norm.NFKC.String(captured) != captured
	n, err := strconv.ParseUint(norm.NFKC.String(captured), 10, 31)
	if err != nil {
		it.Number, it.Unmatched, it.ReasonCode = 0, NotInteger(captured), ReasonInvalidNumber
	} else {
		it.Number, it.Unmatched = int(n), ""
		t.RawNumber, t.AdjustedNumber = new(int64(n)), new(int64(n))
	}
	return it
}

type episodeMatch struct {
	rule     int
	captured string
	season   string
}

// findLast 在 s 里用每一条正则匹配，返回结束得最靠后的集号；一样靠后时取排在前面的正则捕获到的。
func (r EpisodeRule) findLast(s string) (episodeMatch, bool) {
	var best episodeMatch
	end := -1
	for i, p := range r.patterns {
		for _, m := range p.re.FindAllStringSubmatchIndex(s, -1) {
			start, stop := m[2*p.group], m[2*p.group+1]
			if start < 0 || stop <= end {
				continue
			}
			best, end = episodeMatch{rule: i, captured: s[start:stop]}, stop
			if group := p.re.SubexpIndex("season"); group > 0 && m[2*group] >= 0 {
				best.season = s[m[2*group]:m[2*group+1]]
			}
		}
	}
	return best, end >= 0
}
