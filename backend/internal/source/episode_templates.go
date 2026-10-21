package source

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type EpisodeTemplate struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Example string `json:"example"`
	Season  bool   `json:"season"`
}

func EpisodeTemplates() []EpisodeTemplate {
	return []EpisodeTemplate{
		{ID: "episodeLabel", Name: "第几话 / 第几集（未标季）", Example: "第1话 转学生来了"},
		{ID: "seasonNumber", Name: "季号＋数字", Example: "第一季01", Season: true},
		{ID: "seasonEpisodeLabel", Name: "季号＋第几话", Example: "第二季 第1话 上一年级了", Season: true},
		{ID: "numberPrefix", Name: "数字＋空格＋标题", Example: "1 洗眼睛"},
		{ID: "numberOnly", Name: "纯数字", Example: "01"},
		{ID: "sxe", Name: "S季号E集号", Example: "S01E03", Season: true},
		{ID: "ep", Name: "英文集号", Example: "EP03"},
	}
}

// RenderEpisodeTemplate is pure; the client always edits the returned snapshot.
func RenderEpisodeTemplate(id string, season *int, adjustment int64) (EpisodePattern, error) {
	if adjustment < -MaxEpisodeNumber || adjustment > MaxEpisodeNumber {
		return EpisodePattern{}, errors.New("集号加减值越界")
	}
	if season != nil && (*season < 1 || *season > 9999) {
		return EpisodePattern{}, errors.New("来源季必须为 1～9999 的整数")
	}
	seasonText := `[0-9一二三四五六七八九十百千零〇两]+`
	sxeSeason := `\d+`
	if season != nil {
		seasonText = fmt.Sprintf(`(?:0*%d|%s)`, *season, chineseNumber(*season))
		sxeSeason = fmt.Sprintf(`0*%d`, *season)
	}
	prefix := `^\s*(?P<season>第\s*` + seasonText + `\s*季)\s*`
	episode := `(?P<episode>\d+)`
	boundary := `(?:$|[\s，,：:；;！!？?（）()【】\[\]])`
	var pattern string
	switch id {
	case "episodeLabel":
		pattern = `^\s*第\s*` + episode + `\s*[话話集]`
	case "seasonNumber":
		pattern = prefix + episode + boundary
	case "seasonEpisodeLabel":
		pattern = prefix + `第\s*` + episode + `\s*[话話集]`
	case "numberPrefix":
		pattern = `^\s*` + episode + `\s+[^0-9\s.\-–—~～]`
	case "numberOnly":
		pattern = `^\s*` + episode + `\s*$`
	case "sxe":
		pattern = `(?i)\bS(?P<season>` + sxeSeason + `)\s*E` + episode + boundary
	case "ep":
		pattern = `(?i)\bEP\s*` + episode + boundary
	default:
		return EpisodePattern{}, errors.New("未知的集号模板")
	}
	if season != nil && id != "seasonNumber" && id != "seasonEpisodeLabel" && id != "sxe" {
		return EpisodePattern{}, errors.New("此模板不支持指定来源季")
	}
	return EpisodePattern{Pattern: pattern, Adjustment: adjustment}, nil
}

func chineseNumber(n int) string {
	digits := []rune("零一二三四五六七八九")
	units := []string{"千", "百", "十", ""}
	divisors := []int{1000, 100, 10, 1}
	var b strings.Builder
	zero := false
	for i, divisor := range divisors {
		d := n / divisor
		n %= divisor
		if d == 0 {
			zero = b.Len() > 0
			continue
		}
		if zero {
			b.WriteRune(digits[0])
			zero = false
		}
		if d != 1 || divisor != 10 || b.Len() != 0 {
			b.WriteRune(digits[d])
		}
		b.WriteString(units[i])
	}
	return b.String()
}

func DefaultEpisodeRuleV2() EpisodeRule {
	c := EpisodeRuleConfig{Version: 2, Scope: "entry", Rules: []EpisodePattern{}, Excludes: NonRegularExcludes()}
	for _, t := range EpisodeTemplates() {
		p, err := RenderEpisodeTemplate(t.ID, nil, 0)
		if err != nil {
			panic(err)
		}
		c.Rules = append(c.Rules, p)
	}
	r, err := ParseEpisodeRuleConfig(c)
	if err != nil {
		panic(err)
	}
	return r
}

func NonRegularExcludes() []EpisodeExclude {
	return []EpisodeExclude{
		{Kind: "nonRegular", Pattern: `(?i)(?:^|[^A-Za-z0-9_])(?:OAD|OVA|SP|PV|OP|ED)(?:\s*\d+)?(?:$|[^A-Za-z0-9_])|特别篇|特別篇|预告|預告`},
	}
}

// TemplateExample shows the requested season and arithmetic without interpreting titles.
func TemplateExample(id string, season *int) string {
	for _, t := range EpisodeTemplates() {
		if t.ID == id {
			if season != nil && t.Season {
				switch id {
				case "seasonNumber":
					return "第" + chineseNumber(*season) + "季01"
				case "seasonEpisodeLabel":
					return "第" + chineseNumber(*season) + "季 第1话"
				case "sxe":
					return "S" + strconv.Itoa(*season) + "E01"
				}
			}
			return t.Example
		}
	}
	return ""
}
