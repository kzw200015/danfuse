package database_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

var discard = slog.New(slog.DiscardHandler)

func tryLease(t *testing.T, pool *pgxpool.Pool, key string) (*database.Lease, bool) {
	t.Helper()
	lease, ok, err := database.TryLease(t.Context(), pool, discard, key)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Cleanup(lease.Release) // 在关闭连接池之前
	}
	return lease, ok
}

// leaseTest 在 synctest 气泡里运行 f：续约的定时器用假时间推进。连接池在气泡里创建、在气泡里关闭。
func leaseTest(t *testing.T, f func(t *testing.T, pool *pgxpool.Pool)) {
	t.Helper()
	cfg := dbtest.Config(t)
	synctest.Test(t, func(t *testing.T) {
		pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		f(t, pool)
	})
}

func TestTryLease(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)

	first, ok := tryLease(t, pool, database.LeaseSync)
	if !ok {
		t.Fatal("第一次应拿到")
	}
	if got := pool.Stat().AcquiredConns(); got != 0 {
		t.Errorf("持有租约时占用 %d 个连接，want 0", got)
	}
	if _, ok := tryLease(t, pool, database.LeaseSync); ok {
		t.Error("租约被占用时不应拿到")
	}
	if _, ok := tryLease(t, pool, database.LeaseSeasonBackfill(1)); !ok {
		t.Error("不同的键互不影响")
	}

	first.Release()
	if err := first.Context().Err(); err == nil {
		t.Error("释放后 Context 应已取消")
	}
	first.Release() // 可以重复调用
	second, ok := tryLease(t, pool, database.LeaseSync)
	if !ok {
		t.Fatal("释放后应能再次拿到")
	}
	if second.Token() <= first.Token() {
		t.Errorf("token = %d，应大于上一次的 %d", second.Token(), first.Token())
	}
}

// TestLeaseRenews 持有期间每 10 秒续约一次，过期时间回到 30 秒之后。
func TestLeaseRenews(t *testing.T) {
	t.Parallel()
	leaseTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		lease, _ := tryLease(t, pool, database.LeaseSync)
		exec(t, pool, `UPDATE leases SET expires_at = now() + interval '1 second'`)

		time.Sleep(11 * time.Second) // 假时间越过 10 秒时续约已经做完
		var left time.Duration
		if err := pool.QueryRow(t.Context(), `SELECT expires_at - now() FROM leases`).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if left < 20*time.Second {
			t.Errorf("续约后剩余 %v，want 接近 30 秒", left)
		}
		if err := lease.Context().Err(); err != nil {
			t.Errorf("续约成功时 Context 被取消：%v", err)
		}
	})
}

// TestLeaseTakenOver 过期的租约被别人接管：新的 token 更大；原持有者下次续约时发现，取消 Context；
// 原持有者释放时不删别人的租约。
func TestLeaseTakenOver(t *testing.T) {
	t.Parallel()
	leaseTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		old, _ := tryLease(t, pool, database.LeaseSync)
		exec(t, pool, `UPDATE leases SET expires_at = now() - interval '1 second'`)
		current, ok := tryLease(t, pool, database.LeaseSync)
		if !ok {
			t.Fatal("过期的租约应能被接管")
		}
		if current.Token() <= old.Token() {
			t.Errorf("接管得到的 token = %d，应大于原来的 %d", current.Token(), old.Token())
		}

		<-old.Context().Done()
		if cause := context.Cause(old.Context()); !errors.Is(cause, database.ErrLeaseLost) || !strings.Contains(cause.Error(), "taken over") {
			t.Errorf("Cause = %v，want 已被接管", cause)
		}
		old.Release()
		if _, ok := tryLease(t, pool, database.LeaseSync); ok {
			t.Error("原持有者释放后，接管者的租约不应被删掉")
		}
		if err := current.Context().Err(); err != nil {
			t.Errorf("接管者的 Context 被取消：%v", err)
		}
	})
}

// TestLeaseRenewFails 续约一直失败（这里让表暂时不存在）：到本地估计的过期时间取消 Context。
func TestLeaseRenewFails(t *testing.T) {
	t.Parallel()
	leaseTest(t, func(t *testing.T, pool *pgxpool.Pool) {
		lease, _ := tryLease(t, pool, database.LeaseSync)
		exec(t, pool, `ALTER TABLE leases RENAME TO leases_gone`)

		time.Sleep(25 * time.Second)
		if err := lease.Context().Err(); err != nil {
			t.Fatalf("还没到过期时间就取消了：%v", err)
		}
		<-lease.Context().Done()
		if cause := context.Cause(lease.Context()); !errors.Is(cause, database.ErrLeaseLost) || !strings.Contains(cause.Error(), "not renewed") {
			t.Errorf("Cause = %v，want 没能续约", cause)
		}
		exec(t, pool, `ALTER TABLE leases_gone RENAME TO leases`)
	})
}

// TestLeaseParentCanceled 传入的 ctx 取消时 Context 随之取消，租约仍持有到 Release。
func TestLeaseParentCanceled(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	ctx, cancel := context.WithCancel(t.Context())
	lease, ok, err := database.TryLease(ctx, pool, discard, database.LeaseSync)
	if err != nil || !ok {
		t.Fatalf("TryLease = %v, %v", ok, err)
	}
	cancel()
	if lease.Context().Err() == nil {
		t.Error("传入的 ctx 取消后 Context 应已取消")
	}
	if _, ok := tryLease(t, pool, database.LeaseSync); ok {
		t.Error("Release 之前租约应仍被持有")
	}
	lease.Release()
	if _, ok := tryLease(t, pool, database.LeaseSync); !ok {
		t.Error("Release 之后应能拿到")
	}
}

func exec(t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}
