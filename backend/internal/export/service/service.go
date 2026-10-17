// Package service 导出的业务层：把绑定保存的弹幕按偏移校正后写成 B 站格式的 XML（bilifmt.Encode），不做跨源去重、不屏蔽。
// 剧、季、集、绑定与弹幕都由本领域 dao 里的查询读取，不调用别的领域的 Service；导出对所有表只读。
// 文件：binding.go 导出一个绑定，filename.go 文件名的规则。
package service

import (
	"context"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/export/dao"
	"github.com/kzw200015/danfuse/backend/internal/infra/httpx/apierr"
)

var errBindingNotFound = apierr.ErrNotFound.WithMessage("绑定不存在")

// Open 导出确定能进行（查找与校验都已通过）、写出任何内容之前，service 调用它恰好一次，给出文件名，拿到写内容的 io.Writer。
// 出错（不存在等）时不调用。调用方在这里输出响应头：在它之前返回的错误照常走统一的错误响应，之后的错误只能中止输出。
//
// 用回调而不是"先返回文件名、再让调用方另调一次写内容"：一次导出的全部读取要在同一个事务里（快照），
// 而事务由 service 用 pgx.BeginTxFunc 开、关，不交给调用方管理；文件名与写出位置之间正好隔着调用方输出响应头这一步。
type Open func(filename string) io.Writer

// Service 导出一个绑定的弹幕。
type Service struct {
	pool *pgxpool.Pool
	q    *dao.Queries
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, q: dao.New(pool)}
}

// snapshot 在一个只读的 REPEATABLE READ 事务里执行 f：一次导出的全部读取看到的是同一时刻的数据，
// 导出途中正好有绑定在重新拉取或被删除时，也不会一部分是旧的、一部分是新的。这是隔离级别默认用 READ COMMITTED 的例外：
// 只读、不加锁，不会与写入事务冲突。PostgreSQL 在事务的第一条语句取快照，所以查找与校验也在 f 里做。
func (s *Service) snapshot(ctx context.Context, f func(q *dao.Queries) error) error {
	return pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		return f(s.q.WithTx(tx))
	})
}
