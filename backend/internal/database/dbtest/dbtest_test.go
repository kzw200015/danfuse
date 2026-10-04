package dbtest_test

import (
	"fmt"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

func TestPoolIsolated(t *testing.T) {
	// 并行的测试各有自己的库：建同名表互不冲突
	t.Run("parallel", func(t *testing.T) {
		for i := range 3 {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				t.Parallel()
				pool := dbtest.Pool(t)
				if _, err := pool.Exec(t.Context(), "CREATE TABLE marker (id int)"); err != nil {
					t.Fatal(err)
				}
			})
		}
	})

	// 测试里的改动不会写回模板库
	pool := dbtest.Pool(t)
	var exists bool
	if err := pool.QueryRow(t.Context(), "SELECT to_regclass('marker') IS NOT NULL").Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("新库里出现了其他测试建的表")
	}
}
