CONFIG ?= configs/config.yaml
# goose CLI 版本与 go.mod 中的库版本保持一致
GOOSE_VERSION := $(shell go list -m -f '{{.Version}}' github.com/pressly/goose/v3)

.PHONY: run build generate sqlc wire lint fmt tidy migration

## 启动服务（启动时自动执行数据库迁移）
run:
	go run ./cmd/server -config $(CONFIG)

build:
	go build -o bin/server ./cmd/server

## 重新生成 sqlc 与 wire 代码
generate: sqlc wire

sqlc:
	sqlc generate

wire:
	go tool wire ./internal/app

lint:
	golangci-lint run ./...

fmt:
	golangci-lint fmt ./...

tidy:
	go mod tidy

## 新建迁移文件：make migration name=add_xxx
migration:
	@test -n "$(name)" || (echo "usage: make migration name=<migration_name>" && exit 1)
	go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION) -dir db/migrations -s create $(name) sql
