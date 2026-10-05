package database_test

import (
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/database"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

func TestTryAdvisoryLock(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	ctx := t.Context()

	tryLock := func(key int64) (func(), bool) {
		t.Helper()
		unlock, ok, err := database.TryAdvisoryLock(ctx, pool, key)
		if err != nil {
			t.Fatal(err)
		}
		return unlock, ok
	}

	unlock, ok := tryLock(database.LockSync)
	if !ok {
		t.Fatal("第一次应拿到锁")
	}
	if got := pool.Stat().AcquiredConns(); got != 1 {
		t.Errorf("持有锁时占用 %d 个连接，want 1", got)
	}

	if _, ok := tryLock(database.LockSync); ok {
		t.Error("锁被占用时不应拿到")
	}
	otherUnlock, ok := tryLock(database.LockSync + 1)
	if !ok {
		t.Error("不同的键互不影响")
	} else {
		otherUnlock()
	}

	unlock()
	if got := pool.Stat().AcquiredConns(); got != 0 {
		t.Errorf("解锁后仍占用 %d 个连接，want 0", got)
	}
	unlock, ok = tryLock(database.LockSync)
	if !ok {
		t.Fatal("解锁后应能再次拿到")
	}
	unlock()
}

// TestTryAdvisoryLockPair 两段式的键：同一个 (命名空间, ID) 互斥，不同的 ID 互不影响，与单个 bigint 的键也互不影响。
func TestTryAdvisoryLockPair(t *testing.T) {
	t.Parallel()
	pool := dbtest.Pool(t)
	ctx := t.Context()

	tryLock := func(id int64) (func(), bool) {
		t.Helper()
		unlock, ok, err := database.TryAdvisoryLockPair(ctx, pool, database.LockSeasonBackfill, id)
		if err != nil {
			t.Fatal(err)
		}
		return unlock, ok
	}

	unlock, ok := tryLock(1)
	if !ok {
		t.Fatal("第一次应拿到锁")
	}
	if _, ok := tryLock(1); ok {
		t.Error("锁被占用时不应拿到")
	}
	other, ok := tryLock(2)
	if !ok {
		t.Error("不同的 ID 互不影响")
	} else {
		other()
	}
	// 单个 bigint 的键里高 32 位与低 32 位恰好等于 (命名空间, 1)，也不冲突
	single, ok, err := database.TryAdvisoryLock(ctx, pool, int64(database.LockSeasonBackfill)<<32|1)
	if err != nil || !ok {
		t.Errorf("单个 bigint 的键 = %v, %v, want 拿到", ok, err)
	} else {
		single()
	}

	unlock()
	if got := pool.Stat().AcquiredConns(); got != 0 {
		t.Errorf("解锁后仍占用 %d 个连接，want 0", got)
	}
	if _, _, err := database.TryAdvisoryLockPair(ctx, pool, database.LockSeasonBackfill, 1<<31); err == nil {
		t.Error("ID 超出 int 的范围时应返回错误")
	}
}
