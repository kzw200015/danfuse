package danmaku

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// maxBlockedWordLength 一条屏蔽词最多的字符数。
const maxBlockedWordLength = 100

// BlockedWordKind 屏蔽词的类型。取值一经发布不能再改：保存在数据库里，也是管理 API 的取值。
type BlockedWordKind string

const (
	BlockedKeyword BlockedWordKind = "keyword" // 关键词：正文与关键词都归一化（同跨源去重）后，包含即命中
	BlockedRegex   BlockedWordKind = "regex"   // 正则：RE2 语法，对原始正文匹配，默认区分大小写
)

// BlockedWord 一条屏蔽词，由 ParseBlockedWord 校验。
type BlockedWord struct {
	Kind    BlockedWordKind
	Pattern string // 去掉首尾空白的原文，保存和显示的都是它
}

// ParseBlockedWord 校验一条屏蔽词：类型是关键词或正则，内容去掉首尾空白后不为空、不超过 100 个字符，正则要合法。
// 错误的内容是给用户看的提示。
func ParseBlockedWord(kind BlockedWordKind, pattern string) (BlockedWord, error) {
	pattern = strings.TrimSpace(pattern)
	switch {
	case kind != BlockedKeyword && kind != BlockedRegex:
		return BlockedWord{}, errors.New("屏蔽词的类型只能是关键词或正则")
	case pattern == "":
		return BlockedWord{}, errors.New("屏蔽词不能为空")
	case utf8.RuneCountInString(pattern) > maxBlockedWordLength:
		return BlockedWord{}, fmt.Errorf("屏蔽词不能超过 %d 个字符", maxBlockedWordLength)
	}
	if kind == BlockedRegex {
		if _, err := regexp.Compile(pattern); err != nil {
			if syntaxErr, ok := errors.AsType[*syntax.Error](err); ok {
				return BlockedWord{}, fmt.Errorf("不是合法的正则：%s", syntaxErr.Code)
			}
			return BlockedWord{}, errors.New("不是合法的正则")
		}
	}
	return BlockedWord{Kind: kind, Pattern: pattern}, nil
}

// Key 判断屏蔽词是否重复用的内容，同类型的 Key 相同即重复：关键词是归一化后的内容，正则是原文。
func (w BlockedWord) Key() string {
	if w.Kind == BlockedKeyword {
		return normalize(w.Pattern)
	}
	return w.Pattern
}

// Blocklist 编译好的一组屏蔽词，交给 Merge 去掉正文命中的弹幕。零值不屏蔽任何弹幕。
type Blocklist struct {
	keywords []string       // 归一化后的关键词
	regex    *regexp.Regexp // 全部正则合成的一条，一条弹幕只匹配一次；没有正则时为 nil
}

// NewBlocklist 编译一组屏蔽词。words 应当都经过 ParseBlockedWord 校验，正则仍编译不了时返回错误。
func NewBlocklist(words []BlockedWord) (Blocklist, error) {
	var b Blocklist
	var patterns []string
	for _, w := range words {
		switch w.Kind {
		case BlockedKeyword:
			b.keywords = append(b.keywords, w.Key())
		case BlockedRegex:
			// 每条包进非捕获组：其中的 (?i) 这类标志只作用于这一条
			patterns = append(patterns, "(?:"+w.Pattern+")")
		default:
			return Blocklist{}, fmt.Errorf("unknown blocked word kind %q", w.Kind)
		}
	}
	if len(patterns) > 0 {
		re, err := regexp.Compile(strings.Join(patterns, "|"))
		if err != nil {
			return Blocklist{}, fmt.Errorf("compile blocked regexes: %w", err)
		}
		b.regex = re
	}
	return b, nil
}

// blocks 一条弹幕是否被屏蔽：text 是原始正文（正则按它匹配），normalized 是它归一化后的形式（关键词按它匹配）。
func (b Blocklist) blocks(text, normalized string) bool {
	for _, k := range b.keywords {
		if strings.Contains(normalized, k) {
			return true
		}
	}
	return b.regex != nil && b.regex.MatchString(text)
}
