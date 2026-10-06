package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 在 sqlc 生成的 Queries 之上提供事务能力，service 层依赖它访问数据库：
// 直接调用的查询各自提交，要原子性的放进 ExecTx。
// 本文件为手写代码，sqlc generate 不会覆盖。
type Store struct {
	*Queries
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{Queries: New(pool), pool: pool}
}

// ExecTx 在事务中执行 fn，fn 内必须使用传入的 q 执行查询。fn 返回 error 或 panic 时回滚，否则提交；
// fn 返回的 error 原样返回。
func (s *Store) ExecTx(ctx context.Context, fn func(q *Queries) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return fn(s.WithTx(tx))
	})
}
