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
