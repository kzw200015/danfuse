// Package db 存放数据库迁移（goose）与查询（sqlc）SQL 文件。
// 迁移文件通过 embed 打包进二进制，服务启动时自动执行；Go 迁移（migrations 包）编译进二进制，由 database 包导入注册。
package db

import "embed"

// Migrations 内嵌整个 migrations 目录，goose 只读取其中以版本号开头的 .sql、.go 文件。
//
//go:embed migrations
var Migrations embed.FS
