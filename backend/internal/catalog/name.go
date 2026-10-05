package catalog

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// ParsedName 从一段名称（搜索关键词、match 的文件名）里认出的季号、集号和其余部分。
type ParsedName struct {
	Title   string // 去掉季号、集号的标注之后的其余部分，交给全文搜索
	Season  *int   // 写明了的季号，特别篇为 0；没有标注的数字（"剧名2"）不算
	Episode *int   // 写明了的集号
}

var (
	seasonEpisodePattern = regexp.MustCompile(`(?i)\bS(\d{1,4}) ?E(\d{1,4})\b`) // S01E11、s1e11、S01 E11
	seasonPatterns       = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bS(\d{1,4})\b`), // S01
		regexp.MustCompile(`第 ?(\d{1,4}) ?季`),    // 第1季
	}
	specialPattern  = regexp.MustCompile(`特[别別]篇`)
	episodePatterns = []*regexp.Regexp{
		regexp.MustCompile(`第 ?(\d{1,4}) ?[话話集]`),   // 第11话、第11話、第11集
		regexp.MustCompile(`(?i)\bEP ?(\d{1,4})\b`), // EP11
	}
)

// ParseName 认出名称里写明了的季号和集号，其余部分作为标题。搜索关键词和 match 的文件名都用它，规则相同：
//   - 季号和集号：S01E11；
//   - 季号：S01、第1季，"特别篇"为第 0 季，与 SeasonName 输出的写法一致；
//   - 集号：第11话（第11話、第11集）、EP11。
//
// 季号、集号各取第一个认出的，认出的部分从标题里去掉。先清洗为 NFKC，全角的数字、字母也能认出。
// 没有标注的数字（"剧名2"、"Mob Psycho 100"）不拆，分不出是标题的一部分还是季号，留给全文搜索（见 SearchVector）。
// 其他内容（字幕组、分辨率、编码）不清理。
func ParseName(name string) ParsedName {
	name = norm.NFKC.String(name)
	var p ParsedName
	if m := seasonEpisodePattern.FindStringSubmatchIndex(name); m != nil {
		p.Season, p.Episode = atoi(name[m[2]:m[3]]), atoi(name[m[4]:m[5]])
		name = cut(name, m)
	}
	if p.Season == nil {
		for _, re := range seasonPatterns {
			if m := re.FindStringSubmatchIndex(name); m != nil {
				p.Season = atoi(name[m[2]:m[3]])
				name = cut(name, m)
				break
			}
		}
	}
	if p.Season == nil {
		if m := specialPattern.FindStringIndex(name); m != nil {
			p.Season = new(0)
			name = cut(name, m)
		}
	}
	if p.Episode == nil {
		for _, re := range episodePatterns {
			if m := re.FindStringSubmatchIndex(name); m != nil {
				p.Episode = atoi(name[m[2]:m[3]])
				name = cut(name, m)
				break
			}
		}
	}
	p.Title = strings.TrimSpace(name)
	return p
}

// cut 去掉 s 里 m[0]:m[1] 这一段，换成空格，免得两边的字连成一个词。
func cut(s string, m []int) string {
	return s[:m[0]] + " " + s[m[1]:]
}

// atoi 正则只匹配 1～4 位数字，不会出错。
func atoi(s string) *int {
	n, _ := strconv.Atoi(s)
	return &n
}
