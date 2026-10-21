package database_test

import (
	"strings"
	"testing"

	"github.com/kzw200015/danfuse/backend/internal/infra/database/dbtest"
)

func TestMigration00011RoundTrip(t *testing.T) {
	t.Parallel()
	m := dbtest.NewMigrator(t)
	m.UpTo(t, 7)
	seed00007(t, m.Pool)
	m.UpTo(t, 8)
	exec(t, m.Pool, `UPDATE season_bindings SET episode_patterns = ARRAY['EP(\d+)', '第(\d+)话'] WHERE id = 2`)

	m.UpTo(t, 11)
	assertRows(t, m.Pool, `SELECT episode_rule FROM season_bindings WHERE id = 2`, `[{
		"episode_rule": {"version": 1, "scope": "full", "rules": [
			{"pattern": "EP(\\d+)", "adjustment": 0},
			{"pattern": "第(\\d+)话", "adjustment": 0}
		], "excludes": []}
	}]`)
	assertRows(t, m.Pool, `SELECT season_binding_id, number, label_parts FROM season_binding_items ORDER BY season_binding_id, position`, `[
		{"season_binding_id": 1, "number": 1, "label_parts": null},
		{"season_binding_id": 1, "number": null, "label_parts": null},
		{"season_binding_id": 2, "number": null, "label_parts": null}
	]`)
	assertRows(t, m.Pool, `SELECT id, season_binding_id, "offset" FROM bindings WHERE id = 3`, `[{"id": 3, "season_binding_id": 1, "offset": 2.5}]`)

	m.DownTo(t, 10)
	assertRows(t, m.Pool, `SELECT episode_patterns FROM season_bindings WHERE id = 2`, `[{"episode_patterns": ["EP(\\d+)", "第(\\d+)话"]}]`)
	m.UpTo(t, 11)
	if _, err := m.Pool.Exec(t.Context(), `UPDATE season_bindings SET episode_rule = $1 WHERE id = 2`, `{"version":2,"scope":"entry","rules":[{"pattern":"(1)","adjustment":0}],"excludes":[]}`); err != nil {
		t.Fatal(err)
	}
	err := m.TryDownTo(t.Context(), 10)
	if err == nil || !strings.Contains(err.Error(), "拒绝有损回退") {
		t.Fatalf("DownTo with v2 = %v", err)
	}
	assertRows(t, m.Pool, `SELECT (episode_rule ->> 'version')::int AS version FROM season_bindings WHERE id = 2`, `[{"version": 2}]`)
	assertRows(t, m.Pool, `SELECT label_parts FROM season_binding_items LIMIT 1`, `[{"label_parts": null}]`)
}
