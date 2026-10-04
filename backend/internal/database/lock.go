package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 应用使用的 PostgreSQL advisory lock 键（单个 bigint 形式），集中登记在这里，避免互相冲突。
// 迁移用的是 goose 自带的锁（键为 lock.DefaultLockID = 4097083626），与这里的键不同。
const (
	// LockSync 同一时间只跑一次同步，多实例同样成立。取值为 "danfuse" 的 ASCII 加序号 01。
	LockSync int64 = 0x64616e6675736501
)

// unlockTimeout 解锁的超时。解锁用独立的 ctx：调用方的 ctx 可能已经取消，锁仍要释放。
const unlockTimeout = 5 * time.Second

// TryAdvisoryLock 从连接池取一个专用连接执行 pg_try_advisory_lock(key)，不等待。
// 拿到锁时一直占用这个连接，直到调用 unlock：先 pg_advisory_unlock 再归还连接；解锁失败时关闭连接
// （会话结束锁随之释放），不让它带着锁回到池里。没拿到锁时 ok 为 false，连接已归还。
func TryAdvisoryLock(ctx context.Context, pool *pgxpool.Pool, key int64) (unlock func(), ok bool, err error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire connection for advisory lock: %w", err)
	}
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", key).Scan(&ok); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("try advisory lock %d: %w", key, err)
	}
	if !ok {
		conn.Release()
		return nil, false, nil
	}

	unlock = func() {
		ctx, cancel := context.WithTimeout(context.Background(), unlockTimeout)
		defer cancel()
		var released bool
		if err := conn.QueryRow(ctx, "SELECT pg_advisory_unlock($1)", key).Scan(&released); err != nil || !released {
			_ = conn.Hijack().Close(ctx) // 连接已脱离连接池，关闭失败也不会再被复用
			return
		}
		conn.Release()
	}
	return unlock, true, nil
}
