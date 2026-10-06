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

// maxPatternLength 集号规则（正则）最多的字符数。
const maxPatternLength = 200

// 认不出集号的原因。
const (
	noEpisodeInName = "名称里认不出集号"
	notMatchPattern = "不符合集号规则"
)

// trailingNumber 名称末尾的数字（前面是开头、空白或"/"），例如"某番 / 01"。
var trailingNumber = regexp.MustCompile(`(?:^|[\s/])(\d{1,4})\s*$`)

// EpisodeRule 集号规则：季绑定从条目名称（标签）认出合集序号的规则，只用于按规则编号的合集（Collection.NumberedByRule）。
// 零值是内置规则：认写明的集号（catalog.ParseName 的第N话、第N集、EPN、S01E11），整个合集都没有写明集号的条目时，
// 改认名称末尾的数字；也可以是一个正则，第一个捕获组为集号。
type EpisodeRule struct {
	re *regexp.Regexp // nil 为内置规则
}

// ParseEpisodeRule 解析季绑定保存的集号规则：空串为内置规则，否则是 RE2 正则，要有捕获组、不超过 200 个字符。
// 错误的内容是给用户看的提示。
func ParseEpisodeRule(pattern string) (EpisodeRule, error) {
	if pattern == "" {
		return EpisodeRule{}, nil
	}
	if utf8.RuneCountInString(pattern) > maxPatternLength {
		return EpisodeRule{}, fmt.Errorf("集号规则不能超过 %d 个字符", maxPatternLength)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		if syntaxErr, ok := errors.AsType[*syntax.Error](err); ok {
			return EpisodeRule{}, fmt.Errorf("集号规则不是合法的正则：%s", syntaxErr.Code)
		}
		return EpisodeRule{}, errors.New("集号规则不是合法的正则")
	}
	if re.NumSubexp() == 0 {
		return EpisodeRule{}, errors.New(`集号规则里要有一个捕获组，例如 第(\d+)集`)
	}
	return EpisodeRule{re: re}, nil
}

// Pattern 保存用的文本，内置规则为空串。
func (r EpisodeRule) Pattern() string {
	if r.re == nil {
		return ""
	}
	return r.re.String()
}

// NumberItems 预览、保存之前整理 ListCollection 列出的条目：按规则编号的合集先用集号规则从标签认出各条目的序号
// （认不出的对不上），再交给 NormalizeItems 去掉重复的 ref、标出重复的序号。不改动传入的切片。
func NumberItems(c Collection, r EpisodeRule) []CollectionItem {
	if !c.NumberedByRule {
		return NormalizeItems(c.Items)
	}
	numbered := make([]CollectionItem, len(c.Items))
	if r.re != nil {
		for i, it := range c.Items {
			numbered[i] = r.matchPattern(it)
		}
		return NormalizeItems(numbered)
	}
	written := make([]*int, len(c.Items))
	anyWritten := false
	for i, it := range c.Items {
		written[i] = catalog.ParseName(it.Label).Episode
		anyWritten = anyWritten || written[i] != nil
	}
	for i, it := range c.Items {
		it.Number, it.Unmatched = 0, noEpisodeInName
		if anyWritten {
			if written[i] != nil {
				it.Number, it.Unmatched = *written[i], ""
			}
		} else if m := trailingNumber.FindStringSubmatch(norm.NFKC.String(it.Label)); m != nil {
			it.Number, it.Unmatched = atoi(m[1]), ""
		}
		numbered[i] = it
	}
	return NormalizeItems(numbered)
}

// matchPattern 用正则认一个条目的序号，按标签原文匹配（用户照着看到的标签写）：匹配不上、或第一个捕获组没有参与匹配时对不上；
// 捕获到的（清洗为 NFKC 之后）不是不小于 0 的整数时，对不上的原因写明捕获到的内容。
func (r EpisodeRule) matchPattern(it CollectionItem) CollectionItem {
	it.Number, it.Unmatched = 0, notMatchPattern
	m := r.re.FindStringSubmatchIndex(it.Label)
	if m == nil || m[2] < 0 {
		return it
	}
	captured := it.Label[m[2]:m[3]]
	n, err := strconv.ParseUint(norm.NFKC.String(captured), 10, 31)
	if err != nil {
		it.Unmatched = fmt.Sprintf("集号「%s」不是整数", captured)
		return it
	}
	it.Number, it.Unmatched = int(n), ""
	return it
}

// atoi 正则只匹配 1～4 位数字，不会出错。
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
