// Package fulltext 目录搜索的分词：在 Go 里生成 tsvector 和 tsquery 的文本，写入和查询都不经过
// PostgreSQL 的分词器，与数据库的 locale 无关，也不需要扩展。纯计算，不依赖任何业务包。
//
// 清洗为 NFKC 再转小写；按字符类别切成拉丁字母串、数字串、中日韩字符串（汉字、假名、谚文），
// 其余字符都是分隔符，字母与数字相接处也切开（overlord2 → overlord、2）。
// 建索引时中日韩字符串产出单字和相邻两字，查询时长度 ≥ 2 的只用相邻两字：输入标题里连续的一段（两字以上）
// 就能命中，不依赖词典。字母串、数字串整串为一个词，整词匹配，不做前缀。
//
// 改动这里的任何规则，都要新增一个重算搜索列的 Go 迁移，见 db/migrations。
package fulltext

import (
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// maxLexemeBytes PostgreSQL 里单个 tsvector、tsquery 词的长度上限（字节），超出时整条语句报错。
const maxLexemeBytes = 2047

// Weight tsvector 的权重档，字母越靠前权重越高。
type Weight byte

const (
	WeightA Weight = 'A'
	WeightB Weight = 'B'
	WeightC Weight = 'C'
)

// Field 一段带权重的待索引文本。
type Field struct {
	Weight Weight
	Text   string
}

// Vector 生成 tsvector 文本，用 `$1::tsvector` 写入。同一个词只保留一次，权重取它出现过的最高档
// （否则原名与剧名相同的剧会因重复计分而排得偏前）。超过 PostgreSQL 长度上限的词丢掉，免得极端的标题让同步失败。
//
// 每个词带上它在文本里的字符序号作为位置：ts_rank 给多个词的查询按词与词之间的距离计分，
// 位置都相同的词之间不计分。各字段的序号接着往后数，中间隔开一位。
func Vector(fields ...Field) string {
	type entry struct {
		pos    int
		weight Weight
	}
	var lexemes []string // 按第一次出现的顺序
	entries := make(map[string]entry)
	add := func(lexeme string, pos int, weight Weight) {
		if len(lexeme) > maxLexemeBytes {
			return
		}
		e, ok := entries[lexeme]
		if !ok {
			lexemes = append(lexemes, lexeme)
		}
		if !ok || weight < e.weight {
			entries[lexeme] = entry{pos: pos, weight: weight}
		}
	}

	offset := 0
	for _, f := range fields {
		text := clean(f.Text)
		for _, t := range tokens(text) {
			pos := offset + t.start + 1 // tsvector 的位置从 1 开始
			if t.class != classCJK {
				add(string(t.text), pos, f.Weight)
				continue
			}
			for i := range t.text {
				add(string(t.text[i]), pos+i, f.Weight)
				if i+1 < len(t.text) {
					add(string(t.text[i:i+2]), pos+i, f.Weight)
				}
			}
		}
		offset += len(text) + 1
	}

	items := make([]string, len(lexemes))
	for i, lexeme := range lexemes {
		e := entries[lexeme]
		items[i] = fmt.Sprintf("'%s':%d%c", lexeme, e.pos, e.weight)
	}
	return strings.Join(items, " ")
}

// Query 生成 tsquery 文本，所有词都要命中（AND），用 `$1::tsquery` 查询。
// 切不出任何词时返回空串，调用方应直接返回空结果；有词超过 PostgreSQL 的长度上限时也返回空串：
// 索引里不会有这样的词，所有词都要命中的查询一定没有结果。
func Query(keyword string) string {
	var terms []string
	seen := make(map[string]bool)
	tooLong := false
	add := func(term string) {
		if len(term) > maxLexemeBytes {
			tooLong = true
		}
		if !seen[term] {
			seen[term] = true
			terms = append(terms, "'"+term+"'")
		}
	}
	for _, t := range tokens(clean(keyword)) {
		if t.class != classCJK || len(t.text) == 1 {
			add(string(t.text))
			continue
		}
		for i := 0; i+1 < len(t.text); i++ {
			add(string(t.text[i : i+2]))
		}
	}
	if tooLong {
		return ""
	}
	return strings.Join(terms, " & ")
}

func clean(s string) []rune {
	return []rune(strings.ToLower(norm.NFKC.String(s)))
}

type class int

const (
	classSeparator class = iota
	classLatin
	classDigit
	classCJK
)

func classOf(r rune) class {
	switch {
	case unicode.Is(unicode.Latin, r):
		return classLatin
	case unicode.IsDigit(r):
		return classDigit
	// 长音符"ー"属于 Common 而不是片假名，但它是假名词的一部分（オーバーロード）
	case unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul), r == 'ー':
		return classCJK
	default:
		return classSeparator
	}
}

// token 一段同类的字符，start 是它在文本里的字符序号。
// 词里只有字母、数字和中日韩字符，放进单引号就是合法的 tsvector、tsquery 词，不需要转义。
type token struct {
	class class
	text  []rune
	start int
}

// tokens 按字符类别切分清洗后的文本，丢掉分隔符。
func tokens(text []rune) []token {
	var result []token
	for i := 0; i < len(text); {
		c := classOf(text[i])
		j := i + 1
		for j < len(text) && classOf(text[j]) == c {
			j++
		}
		if c != classSeparator {
			result = append(result, token{class: c, text: text[i:j], start: i})
		}
		i = j
	}
	return result
}
