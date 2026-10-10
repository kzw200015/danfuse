package binding_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/binding/bindingdb"
	"github.com/kzw200015/danfuse/backend/internal/database/dbtest"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// getBinding 读出库里的绑定。
func getBinding(t *testing.T, pool *pgxpool.Pool, id int64) bindingdb.Binding {
	t.Helper()
	b, err := bindingdb.New(pool).GetBinding(t.Context(), id)
	if err != nil {
		t.Fatalf("GetBinding(%d): %v", id, err)
	}
	return b
}
