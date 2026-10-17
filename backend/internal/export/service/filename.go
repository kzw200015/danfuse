package service

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/kzw200015/danfuse/backend/internal/naming"
)

// maxNameBytes 文件名、目录名在扩展名（和重名后缀）之前那部分的字节上限。常见的文件系统一个名称最多 255 字节，
// 留出加重名后缀（" (2)"）和扩展名的余量。
const maxNameBytes = 200

// unsafeNameChars 各操作系统里不能出现在文件名、目录名里的字符（Windows 最严），连同控制字符一起换成 _。
const unsafeNameChars = `/\:*?"<>|`

// fallbackName 绑定标题、电影的片名为空（或只有空白）时用的名称。
const fallbackName = "弹幕"

// bindingName 一个绑定导出的 XML 的文件名（不带扩展名，由调用方加 .xml）：绑定标题，标题为空（或只有空白）时用"弹幕"。
// 文件名就是标题，所以这份文件在集面板上传回去时绑定标题不变。
func bindingName(title string) string {
	if strings.TrimSpace(title) == "" {
		return fallbackName
	}
	return safeName(title)
}

// safeName 把 s 整理成能用作文件名、目录名的名称：unsafeNameChars 与控制字符换成 _，
// 超过 maxNameBytes 字节时截断，不切开 UTF-8 字符。
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(unsafeNameChars, r) {
			return '_'
		}
		return r
	}, s)
	if len(s) <= maxNameBytes {
		return s
	}
	n := maxNameBytes
	for !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// fileNames 同一目录下已经用了的文件名。
type fileNames map[string]bool

// add 绑定标题为 title 的 XML 在这个目录下的文件名，记为已用：bindingName 加 .xml，已经用了时依次加 " (2)"、" (3)"，
// 直到没用过（标题本身就是"OP (2)"的绑定，名称被前面的第二个"OP"占了时用"OP (2) (2)"）。按绑定的创建顺序调用，编号就是稳定的。
func (n fileNames) add(title string) string {
	base := bindingName(title)
	name := base + ".xml"
	for i := 2; n[name]; i++ {
		name = fmt.Sprintf("%s (%d).xml", base, i)
	}
	n[name] = true
	return name
}

// episodeZipName 一集导出的 zip 的文件名：<剧名> S01E03.zip（withSeriesTitle、episodeCode）；电影为 <片名>.zip，
// 和界面上电影不显示季号集号一致，片名为空（或只有空白）时用"弹幕"。
func episodeZipName(seriesTitle string, seriesType naming.SeriesType, season, episode int32) string {
	if seriesType != naming.TypeMovie {
		return withSeriesTitle(seriesTitle, episodeCode(season, episode)) + ".zip"
	}
	if strings.TrimSpace(seriesTitle) == "" {
		return fallbackName + ".zip"
	}
	return safeName(seriesTitle) + ".zip"
}

// seasonDirName 一季导出的 zip 的文件名（加 .zip）与里面的根目录名：<剧名> S01（withSeriesTitle、seasonCode）。
func seasonDirName(seriesTitle string, season int32) string {
	return withSeriesTitle(seriesTitle, seasonCode(season))
}

// withSeriesTitle 剧名加季号集号：<剧名> S01E03；剧名为空（或只有空白）时只有季号集号，不以空格开头。
func withSeriesTitle(seriesTitle, code string) string {
	if strings.TrimSpace(seriesTitle) == "" {
		return code
	}
	return safeName(seriesTitle) + " " + code
}

// episodeCode 一集的季号集号：S01E03，补到两位（特别篇为 S00），超过两位照常写（E100）。
// 默认集号规则认得出这个写法，季导出的子目录名也用它。
func episodeCode(season, episode int32) string {
	return fmt.Sprintf("%sE%02d", seasonCode(season), episode)
}

// seasonCode 季号：S01，补到两位（特别篇为 S00），超过两位照常写。
func seasonCode(season int32) string {
	return fmt.Sprintf("S%02d", season)
}
