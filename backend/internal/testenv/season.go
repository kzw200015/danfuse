package testenv

import (
	"crypto/sha256"
	"path"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/danmakufile"
	"github.com/kzw200015/danfuse/backend/internal/source"
)

// InsertCollectionSeasonBinding 直接写入一个合集的季绑定（不列出合集、不补建）：适配器 fake，合集 ref 为 {"list":"<name>"}，
// 链接 list/<name>，标题为"合集 <name>"，默认的集号规则，合集第 from 集为本地第 to 集，追更开着。返回它的 ID。
func InsertCollectionSeasonBinding(t testing.TB, pool *pgxpool.Pool, seasonID int64, name string, from, to int) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(t.Context(), `
		INSERT INTO season_bindings (season_id, kind, adapter, ref, link, title, mapping_from, mapping_to, episode_rule, numbered_by_rule)
		VALUES ($1, 'collection', 'fake', jsonb_build_object('list', $2::text), 'list/' || $2, '合集 ' || $2, $3, $4, $5, false)
		RETURNING id`, seasonID, name, from, to, source.DefaultEpisodeRule().Config()).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// FolderEntry 文件夹的季绑定里的一个条目：名称（不带文件夹名）和它的弹幕文件。
type FolderEntry struct {
	Label string
	Files []danmakufile.File
}

// InsertFolderSeasonBinding 直接写入一个文件夹的季绑定（标题为文件夹名 title）和它的条目、条目的文件，默认的集号规则，
// 第 from 集为本地第 to 集；不建绑定。返回季绑定的 ID 和各条目的 ID（与 entries 一一对应）。
func InsertFolderSeasonBinding(t testing.TB, pool *pgxpool.Pool, seasonID int64, title string, from, to int, entries ...FolderEntry) (int64, []int64) {
	t.Helper()
	var id int64
	err := pool.QueryRow(t.Context(), `
		INSERT INTO season_bindings (season_id, kind, title, follow, mapping_from, mapping_to, episode_rule)
		VALUES ($1, 'folder', $2, false, $3, $4, $5)
		RETURNING id`, seasonID, title, from, to, source.DefaultEpisodeRule().Config()).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, len(entries))
	for i, e := range entries {
		err := pool.QueryRow(t.Context(), `
			INSERT INTO season_binding_entries (season_binding_id, label, position) VALUES ($1, $2, $3) RETURNING id`,
			id, e.Label, i+1).Scan(&ids[i])
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range e.Files {
			sum := sha256.Sum256(f.Data)
			_, err := pool.Exec(t.Context(), `
				INSERT INTO season_binding_entry_files (entry_id, name, sha256, size, content) VALUES ($1, $2, $3, $4, $5)`,
				ids[i], path.Base(f.Name), sum[:], len(f.Data), f.Data)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	return id, ids
}

// InTx 在一个事务里执行 f（例如调用 binding.Service 的 XxxSlotInTx），返回 f 的错误或提交的错误。
func InTx(t testing.TB, pool *pgxpool.Pool, f func(tx pgx.Tx) error) error {
	t.Helper()
	return pgx.BeginFunc(t.Context(), pool, f)
}
