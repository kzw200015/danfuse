// Package db 存放数据库迁移（goose）与查询（sqlc）SQL 文件。
// 迁移文件通过 embed 打包进二进制，服务启动时自动执行。
package db

import "embed"

//go:embed migrations/*.sql
var Migrations embed.FS
