package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 应用的锁是 leases 表里的租约（docs/adr/0003）：拿锁是一条只在过期后才能接管的 upsert，持有者每 leaseRenewInterval
// 续约一次，leaseTTL 内没有续约就过期，过期按数据库的 now() 判断。持锁期间不占用连接。
// 迁移用的是 goose 自带的表锁（goose_lock 表，租约时长与续约间隔与这里相同），与 leases 表无关。
const (
	leaseTTL           = 30 * time.Second
	leaseRenewInterval = 10 * time.Second
	leaseQueryTimeout  = 5 * time.Second // 续约、释放各自的超时
)

// 租约的键，集中登记在这里，避免互相冲突。
const (
	// LeaseSync 同一时间只跑一次同步，多实例同样成立。
	LeaseSync = "sync"
	// LeaseScheduledFetch 同一时间只跑一轮定时拉取，多实例同样成立。
	LeaseScheduledFetch = "scheduled_fetch"
	// LeaseSeasonBackfillPrefix 按季绑定的键的前缀，见 LeaseSeasonBackfill；SQL 里判断"正在补建"时用它拼出同样的键。
	LeaseSeasonBackfillPrefix = "season_backfill:"
)

// LeaseSeasonBackfill 同一个季绑定同一时间只跑一次补建，多实例同样成立；是否正在补建也以它为准。
func LeaseSeasonBackfill(id int64) string {
	return LeaseSeasonBackfillPrefix + strconv.FormatInt(id, 10)
}

// ErrLeaseLost Lease.Context 因租约丢失被取消时的原因（context.Cause）：续约发现租约已被接管，
// 或者续约一直失败，直到本地估计的过期时间。
var ErrLeaseLost = errors.New("lease lost")

// acquireSQL 键不存在、或者已经过期时拿到锁，换上新的 token；否则没有行。
// 没拿到时 nextval 也会消耗一个值，token 只需单调递增，不要求连续。
const acquireSQL = `
INSERT INTO leases AS l (key, token, expires_at)
VALUES ($1, nextval('lease_tokens'), now() + make_interval(secs => $2))
ON CONFLICT (key) DO UPDATE
SET token      = excluded.token,
    expires_at = excluded.expires_at
WHERE l.expires_at <= now()
RETURNING token`

// renewSQL 只续自己的 token；已被接管时更新 0 行。已过期但还没人接管时照样续上，那期间没有别人持有锁。
const renewSQL = `UPDATE leases SET expires_at = now() + make_interval(secs => $3) WHERE key = $1 AND token = $2`

// releaseSQL 只删自己的 token，已被接管时什么都不做。
const releaseSQL = `DELETE FROM leases WHERE key = $1 AND token = $2`

// Lease 拿到的一个租约。持有期间在后台续约，直到 Release；租约丢失时取消 Context。
type Lease struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	key    string
	token  int64

	ctx    context.Context
	cancel context.CancelCauseFunc
	stop   chan struct{} // Release 时关闭，续约随即停下
	done   chan struct{} // 续约停下时关闭
	once   sync.Once
}

// TryLease 试拿 key 的租约，不等待：拿到时 ok 为 true，返回的 Lease 在后台续约，用完必须调用 Release；
// 键被别人持有且没有过期时 ok 为 false。
func TryLease(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, key string) (lease *Lease, ok bool, err error) {
	start := time.Now()
	var token int64
	err = pool.QueryRow(ctx, acquireSQL, key, leaseTTL.Seconds()).Scan(&token)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("acquire lease %s: %w", key, err)
	}

	lease = &Lease{
		pool:   pool,
		logger: logger,
		key:    key,
		token:  token,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	lease.ctx, lease.cancel = context.WithCancelCause(ctx)
	go lease.renew(start.Add(leaseTTL))
	return lease, true, nil
}

// Context 从 TryLease 传入的 ctx 派生：那个 ctx 取消、租约丢失（原因为 ErrLeaseLost）或 Release 之后取消。
// 持锁做的事都用它，租约丢失时随即停下。
func (l *Lease) Context() context.Context { return l.ctx }

// Token 这次拿锁得到的 fencing token，比之前任何一次拿锁得到的都大。
func (l *Lease) Token() int64 { return l.token }

// Release 停止续约、删除租约，取消 Context。用独立的超时 ctx，调用方的 ctx 已经取消时也能释放；
// 释放失败只记日志，租约到时过期。可以重复调用。
func (l *Lease) Release() {
	l.once.Do(func() {
		close(l.stop)
		<-l.done
		l.cancel(nil)

		ctx, cancel := context.WithTimeout(context.Background(), leaseQueryTimeout)
		defer cancel()
		if _, err := l.pool.Exec(ctx, releaseSQL, l.key, l.token); err != nil {
			l.logger.Warn("release lease failed", "key", l.key, "error", err)
		}
	})
}

// renew 每 leaseRenewInterval 续约一次，直到 Release。deadline 是本地估计的过期时间：上一次续约成功（或拿到锁）的
// 那条语句发出时加上 leaseTTL，比数据库里记的过期时间早。续约出错时只记日志、下次再试；到了 deadline 还没续上，
// 或者发现已被接管，取消 Context。
func (l *Lease) renew(deadline time.Time) {
	defer close(l.done)
	ticker := time.NewTicker(leaseRenewInterval)
	defer ticker.Stop()
	expired := time.NewTimer(time.Until(deadline))
	defer expired.Stop()

	for {
		select {
		case <-l.stop:
			return
		case <-expired.C:
			l.cancel(fmt.Errorf("%w: %s: not renewed before expiry", ErrLeaseLost, l.key))
			return
		case <-ticker.C:
		}

		start := time.Now()
		renewed, err := l.extend()
		switch {
		case err != nil:
			l.logger.Warn("renew lease failed", "key", l.key, "error", err)
		case !renewed:
			l.cancel(fmt.Errorf("%w: %s: taken over", ErrLeaseLost, l.key))
			return
		default:
			expired.Reset(time.Until(start.Add(leaseTTL)))
		}
	}
}

// extend 续约一次，renewed 为 false 表示租约已被接管。
func (l *Lease) extend() (renewed bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), leaseQueryTimeout)
	defer cancel()
	tag, err := l.pool.Exec(ctx, renewSQL, l.key, l.token, leaseTTL.Seconds())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
