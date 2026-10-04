// Package web 存放管理界面的前端构建产物，embed 进二进制，由后端与 /api 同源托管。
package web

import (
	"embed"
	"io/fs"
)

// static 下有两个目录：dist/ 是 frontend 执行 pnpm build 的输出，不进 git；
// placeholder/ 是提交进仓库的占位页，保证没有构建前端时也能编译。
// all: 前缀让以 . 或 _ 开头的产物文件也被内嵌。
//
//go:embed all:static
var static embed.FS

// FS 返回要托管的前端文件，根目录下是 index.html 与 assets/。
// 没有构建前端时返回占位页，访问管理界面只显示一行"前端未构建"。
func FS() fs.FS {
	dir := "static/dist"
	if _, err := fs.Stat(static, dir+"/index.html"); err != nil {
		dir = "static/placeholder"
	}
	sub, _ := fs.Sub(static, dir) // dir 是合法路径，fs.Sub 不会出错
	return sub
}
