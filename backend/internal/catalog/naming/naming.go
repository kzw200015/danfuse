// Package naming 认名称（搜索关键词、弹弹 API match 的文件名）里写明的季号、集号。纯计算，目录的搜索和识别、
// 季绑定集号规则的默认值（source.DefaultEpisodeRule）都用它。
package naming

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Parsed 从一段名称（搜索关键词、match 的文件名）里认出的季号、集号和其余部分。
type Parsed struct {
	Title   string // 去掉季号、集号的标注之后的其余部分，交给全文搜索
	Season  *int   // 写明了的季号，特别篇为 0；没有标注的数字（"剧名2"）不算
	Episode *int   // 写明了的集号
}

// namePattern 名称里季号、集号的一种写法：命名捕获组 season、episode 标出季号、集号。
// 有的写法季号是写死的（"特别篇"为第 0 季），这时正则里没有 season 组，季号为 fixedSeason。
type namePattern struct {
	re           *regexp.Regexp
	fixedSeason  *int
	seasonGroup  int // season 组的下标，没有时为 -1
	episodeGroup int // episode 组的下标，没有时为 -1
}

func newNamePattern(expr string, fixedSeason *int) namePattern {
	re := regexp.MustCompile(expr)
	return namePattern{re: re, fixedSeason: fixedSeason, seasonGroup: re.SubexpIndex("season"), episodeGroup: re.SubexpIndex("episode")}
}

// namePatterns 按优先级排列的写法。Parse 依次试：一种写法只在它给出的季号、集号都还没认出时才试。
var namePatterns = []namePattern{
	newNamePattern(`(?i)\bS(?P<season>\d{1,4}) ?E(?P<episode>\d{1,4})\b`, nil), // S01E11、s1e11、S01 E11
	newNamePattern(`(?i)\bS(?P<season>\d{1,4})\b`, nil),                        // S01
	newNamePattern(`第 ?(?P<season>\d{1,4}) ?季`, nil),                           // 第1季
	newNamePattern(`特[别別]篇`, new(0)),                                           // 特别篇为第 0 季
	newNamePattern(`第 ?(?P<episode>\d{1,4}) ?[话話集]`, nil),                      // 第11话、第11話、第11集
	newNamePattern(`(?i)\bEP ?(?P<episode>\d{1,4})\b`, nil),                    // EP11
}

// EpisodePatterns 写明集号的写法（namePatterns 里带 episode 组的），按优先级排列。季绑定集号规则的默认值取自这里。
func EpisodePatterns() []string {
	var patterns []string
	for _, np := range namePatterns {
		if np.episodeGroup > 0 {
			patterns = append(patterns, np.re.String())
		}
	}
	return patterns
}

// Parse 认出名称里写明了的季号和集号，其余部分作为标题。搜索关键词和 match 的文件名都用它，规则相同：
//   - 季号和集号：S01E11；
//   - 季号：S01、第1季，"特别篇"为第 0 季，与 catalog.SeasonName 输出的写法一致；
//   - 集号：第11话（第11話、第11集）、EP11。
//
// 按 namePatterns 的顺序认，季号、集号各取第一个认出的，认出的部分从标题里去掉。先清洗为 NFKC，全角的数字、字母也能认出。
// 没有标注的数字（"剧名2"、"Mob Psycho 100"）不拆，分不出是标题的一部分还是季号，留给全文搜索（见 catalog.SearchVector）。
// 其他内容（字幕组、分辨率、编码）不清理。
func Parse(name string) Parsed {
	name = norm.NFKC.String(name)
	var p Parsed
	for _, np := range namePatterns {
		givesSeason, givesEpisode := np.seasonGroup > 0 || np.fixedSeason != nil, np.episodeGroup > 0
		if givesSeason && p.Season != nil || givesEpisode && p.Episode != nil {
			continue
		}
		m := np.re.FindStringSubmatchIndex(name)
		if m == nil {
			continue
		}
		switch {
		case np.fixedSeason != nil:
			p.Season = new(*np.fixedSeason)
		case givesSeason:
			p.Season = atoi(name[m[2*np.seasonGroup]:m[2*np.seasonGroup+1]])
		}
		if givesEpisode {
			p.Episode = atoi(name[m[2*np.episodeGroup]:m[2*np.episodeGroup+1]])
		}
		name = cut(name, m)
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
