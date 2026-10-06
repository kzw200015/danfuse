package source

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strconv"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/kzw200015/danfuse/backend/internal/catalog"
)

const (
	maxPatterns      = 10  // 集号规则最多的条数
	maxPatternLength = 200 // 一条正则最多的字符数
)

// notMatchPattern 每一条正则都匹配不上时对不上的原因。
const notMatchPattern = "不符合集号规则"

// EpisodeRule 集号规则：季绑定从条目名称（标签）认出合集序号的规则，只用于按规则编号的合集（Collection.NumberedByRule）。
// 是一组按优先级排列的正则：每个条目依次试，第一条匹配上的给出集号；有名为 episode 的捕获组时取它，否则取第一个捕获组。
// 由 ParseEpisodeRule 或 DefaultEpisodeRule 得到，零值不可用。
type EpisodeRule struct {
	patterns []episodePattern
}

type episodePattern struct {
	re    *regexp.Regexp
	group int // 集号所在捕获组的下标
}

// defaultRule 默认规则：catalog.EpisodePatterns，与搜索、match 认集号的写法相同。
var defaultRule = func() EpisodeRule {
	r, err := ParseEpisodeRule(catalog.EpisodePatterns())
	if err != nil {
		panic(err)
	}
	return r
}()

// DefaultEpisodeRule 默认规则。季绑定保存的是创建时的副本，之后默认规则变了也不影响已有的季绑定。
func DefaultEpisodeRule() EpisodeRule {
	return defaultRule
}

// ParseEpisodeRule 解析一组按优先级排列的正则：至少 1 条、至多 10 条，每条是 RE2 正则，要有捕获组、不超过 200 个字符。
// 错误的内容是给用户看的提示。
func ParseEpisodeRule(patterns []string) (EpisodeRule, error) {
	switch {
	case len(patterns) == 0:
		return EpisodeRule{}, errors.New("至少要有一条集号规则")
	case len(patterns) > maxPatterns:
		return EpisodeRule{}, fmt.Errorf("集号规则最多 %d 条", maxPatterns)
	}
	r := EpisodeRule{patterns: make([]episodePattern, len(patterns))}
	for i, pattern := range patterns {
		p, err := parsePattern(pattern)
		if err != nil {
			return EpisodeRule{}, fmt.Errorf("第 %d 条集号规则%w", i+1, err)
		}
		r.patterns[i] = p
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

// NumberItems 预览、保存之前整理 ListCollection 列出的条目：按规则编号的合集先用集号规则从标签认出各条目的序号
// （认不出的对不上），再交给 NormalizeItems 去掉重复的 ref、标出重复的序号。不改动传入的切片。
func NumberItems(c Collection, r EpisodeRule) []CollectionItem {
	if !c.NumberedByRule {
		return NormalizeItems(c.Items)
	}
	numbered := make([]CollectionItem, len(c.Items))
	for i, it := range c.Items {
		numbered[i] = r.match(it)
	}
	return NormalizeItems(numbered)
}

// match 认一个条目的序号：按优先级逐条试，每条先按标签原文匹配（用户照着看到的标签写），匹配不上、且清洗为 NFKC 后有变化时
// 再按清洗后的标签匹配（全角的数字、字母也能认出）；集号的捕获组没有参与匹配时算这一条匹配不上。第一条匹配上的为准：
// 捕获到的（清洗为 NFKC 之后）不是不小于 0 的整数时对不上，原因写明捕获到的内容，不再试后面的。
func (r EpisodeRule) match(it CollectionItem) CollectionItem {
	normalized := norm.NFKC.String(it.Label)
	for _, p := range r.patterns {
		captured, ok := p.find(it.Label)
		if !ok && normalized != it.Label {
			captured, ok = p.find(normalized)
		}
		if !ok {
			continue
		}
		n, err := strconv.ParseUint(norm.NFKC.String(captured), 10, 31)
		if err != nil {
			it.Number, it.Unmatched = 0, fmt.Sprintf("集号「%s」不是整数", captured)
		} else {
			it.Number, it.Unmatched = int(n), ""
		}
		return it
	}
	it.Number, it.Unmatched = 0, notMatchPattern
	return it
}

// find 在 s 里匹配，返回集号的捕获组捕获到的内容。
func (p episodePattern) find(s string) (string, bool) {
	m := p.re.FindStringSubmatchIndex(s)
	if m == nil || m[2*p.group] < 0 {
		return "", false
	}
	return s[m[2*p.group]:m[2*p.group+1]], true
}
