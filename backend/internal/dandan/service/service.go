// Package service 弹弹 API 的业务层：识别、搜索、取季与取一集合并后的弹幕。返回的类型是内部结构，不含任何协议格式，
// 协议的结构与转换在同级的 handler 包；查询（搜索季、取季和集）在 dao 包，读的是目录的表。
// 文件：search.go 搜索、match.go 按文件名识别、season.go 取季（季与集的结构）、comment.go 取弹幕。
package service

import (
	"github.com/jackc/pgx/v5/pgxpool"

	bindingsvc "github.com/kzw200015/danfuse/backend/internal/binding/service"
	blockwordsvc "github.com/kzw200015/danfuse/backend/internal/blockword/service"
	"github.com/kzw200015/danfuse/backend/internal/dandan/dao"
)

// Service 弹弹 API 背后的查询：在目录里搜索季、按名称识别一集、按 ID 取季，读取一集所有绑定的弹幕。
// 返回的是系统内部的结构，不是弹弹play 的 JSON：协议参数的转换与格式化归 handler。
type Service struct {
	q            *dao.Queries
	bindings     *bindingsvc.Service   // 一集各绑定的弹幕
	blockedWords *blockwordsvc.Service // 取弹幕时要去掉的屏蔽词
}

func NewService(pool *pgxpool.Pool, bindings *bindingsvc.Service, blockedWords *blockwordsvc.Service) *Service {
	return &Service{q: dao.New(pool), bindings: bindings, blockedWords: blockedWords}
}

func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	return new(int32(*v))
}

// emptyIfNull null 读作空串。
func emptyIfNull(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
