package service

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// maxNameBytes 文件名、目录名在扩展名（和重名后缀）之前那部分的字节上限。常见的文件系统一个名称最多 255 字节，
// 留出加重名后缀（" (2)"）和扩展名的余量。
const maxNameBytes = 200

// unsafeNameChars 各操作系统里不能出现在文件名、目录名里的字符（Windows 最严），连同控制字符一起换成 _。
const unsafeNameChars = `/\:*?"<>|`

// bindingFileName 一个绑定导出的 XML 的文件名：绑定标题加 .xml，标题为空（或只有空白）时用"弹幕"。
// 文件名就是标题，所以这份文件在集面板上传回去时绑定标题不变。
func bindingFileName(title string) string {
	if strings.TrimSpace(title) == "" {
		title = "弹幕"
	}
	return safeName(title) + ".xml"
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
