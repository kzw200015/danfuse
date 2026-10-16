package database_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kzw200015/danfuse/backend/internal/infra/database/dbtest"
	"github.com/kzw200015/danfuse/backend/internal/infra/testutil"
)

// 迁移 00008（重写季绑定，见 docs/adr/0010）：在 00007 上造出老数据，执行 00008 核对 Up 的每一步，再 Down 回 00007。

// seed00007 在 00007 的表结构上造出老数据：
//   - 合集的季绑定 A（番剧，平台给集号）：第 1 集上有两个建出的绑定（槽位冲突），第 2 集一个，有处理过的记录；
//   - 合集的季绑定 B（投稿合集）、C（多 P 投稿），按规则编号，条目有认出的序号；
//   - 文件夹的季绑定 F：第 3、4 集各有一个建出的文件绑定，标题带"F / "前缀，原文件挂在绑定下；
//   - 文件夹的季绑定 G：建出的绑定已被删光；
//   - 一个普通的文件绑定（当初删季绑定时被置空来源）。
func seed00007(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec(t, pool, `
INSERT INTO series (type, title) VALUES ('tv', '剧');
INSERT INTO seasons (series_id, number) VALUES (1, 1);
INSERT INTO episodes (season_id, number) VALUES (1, 1), (1, 2), (1, 3), (1, 4);

INSERT INTO season_bindings (season_id, kind, adapter, ref, title, finished, mapping_from, mapping_to, episode_patterns, numbered_by_rule)
VALUES (1, 'collection', 'bilibili', '{"kind":"bangumi","seasonId":101}', 'A', true, 1, 1, '{"(\\d+)"}', false),
       (1, 'collection', 'bilibili', '{"kind":"ugcSeason","seasonId":202,"mid":7}', 'B', false, 1, 1, '{"(\\d+)"}', true),
       (1, 'collection', 'bilibili', '{"kind":"multiPage","aid":303}', 'C', false, 1, 1, '{"(\\d+)"}', true);
INSERT INTO season_bindings (season_id, kind, title, follow) VALUES (1, 'folder', 'F', false), (1, 'folder', 'G', false);

INSERT INTO season_binding_items (season_binding_id, ref, position, number, unmatched_reason, label)
VALUES (1, '{"kind":"episode","epId":11}', 1, 1, NULL, '第1话'),
       (1, '{"kind":"episode","epId":12}', 2, NULL, '平台没有给出整数集号', 'PV'),
       (2, '{"kind":"video","aid":21,"page":1}', 1, 3, NULL, 'EP03');

-- 绑定 1、2 是 A 在第 1 集上的槽位冲突，3 是 A 在第 2 集上的
INSERT INTO bindings (episode_id, adapter, ref, title, duration, season_binding_id, "offset")
VALUES (1, 'bilibili', '{"kind":"episode","epId":10}', '旧稿', 1440, 1, 0),
       (1, 'bilibili', '{"kind":"episode","epId":11}', '第1话', 1440, 1, 0),
       (2, 'bilibili', '{"kind":"episode","epId":12}', '第2话', 1440, 1, 2.5);
INSERT INTO season_binding_handled (season_binding_id, ref, episode_id)
VALUES (1, '{"kind":"episode","epId":10}', 1), (1, '{"kind":"episode","epId":11}', 1), (1, '{"kind":"episode","epId":12}', 2);

-- 绑定 4、5 是 F 建出的文件绑定，6 是普通的文件绑定
INSERT INTO bindings (episode_id, kind, title, season_binding_id, file_count, "offset")
VALUES (4, 'file', 'F / 04', 4, 1, 0),
       (3, 'file', 'F / 03', 4, 2, 1.5),
       (1, 'file', '手动上传', NULL, 1, 0);
INSERT INTO binding_files (binding_id, name, sha256, size, content)
VALUES (4, '04.xml', '\x04', 3, 'd04'),
       (5, 'a.xml', '\x0a', 3, 'd3a'),
       (5, 'b.xml', '\x0b', 3, 'd3b'),
       (6, 'manual.xml', '\x06', 3, 'dmm');
`)
}

func TestMigration00008Up(t *testing.T) {
	t.Parallel()
	m := dbtest.NewMigrator(t)
	m.UpTo(t, 7)
	seed00007(t, m.Pool)

	m.UpTo(t, 8)

	// 合集的季绑定按 ref 拼出链接；文件夹的季绑定填上默认规则的快照和 1 = 1，没有条目的 G 被删掉
	assertRows(t, m.Pool, `SELECT id, kind, link, mapping_from, mapping_to, episode_patterns FROM season_bindings ORDER BY id`, `[
		{"id": 1, "kind": "collection", "link": "https://www.bilibili.com/bangumi/play/ss101", "mapping_from": 1, "mapping_to": 1, "episode_patterns": ["(\\d+)"]},
		{"id": 2, "kind": "collection", "link": "https://space.bilibili.com/7/lists/202?type=season", "mapping_from": 1, "mapping_to": 1, "episode_patterns": ["(\\d+)"]},
		{"id": 3, "kind": "collection", "link": "https://www.bilibili.com/video/av303", "mapping_from": 1, "mapping_to": 1, "episode_patterns": ["(\\d+)"]},
		{"id": 4, "kind": "folder", "link": null, "mapping_from": 1, "mapping_to": 1, "episode_patterns": [
			"(?i)\\bS(?P<season>\\d{1,4}) ?E(?P<episode>\\d{1,4})\\b",
			"第 ?(?P<episode>\\d{1,4}) ?[话話集]",
			"(?i)\\bEP ?(?P<episode>\\d{1,4})\\b",
			"/ (?P<episode>\\d{1,4})$"
		]}
	]`)

	// 按规则编号的合集的条目清空集号，番剧的保留平台给的集号
	assertRows(t, m.Pool, `SELECT season_binding_id, label, number FROM season_binding_items ORDER BY season_binding_id, position`, `[
		{"season_binding_id": 1, "label": "第1话", "number": 1},
		{"season_binding_id": 1, "label": "PV", "number": null},
		{"season_binding_id": 2, "label": "EP03", "number": null}
	]`)

	// 槽位冲突留 id 最大的；F 的绑定指向各自的条目，标题去掉"F / "，原文件移走、份数归零；普通的文件绑定不动
	assertRows(t, m.Pool, `
SELECT b.id, b.season_binding_id, e.label AS entry, b.title, b.file_count, b."offset"
FROM bindings b LEFT JOIN season_binding_entries e ON e.id = b.entry_id ORDER BY b.id`, `[
		{"id": 1, "season_binding_id": null, "entry": null, "title": "旧稿", "file_count": 0, "offset": 0},
		{"id": 2, "season_binding_id": 1, "entry": null, "title": "第1话", "file_count": 0, "offset": 0},
		{"id": 3, "season_binding_id": 1, "entry": null, "title": "第2话", "file_count": 0, "offset": 2.5},
		{"id": 4, "season_binding_id": 4, "entry": "04", "title": "04", "file_count": 0, "offset": 0},
		{"id": 5, "season_binding_id": 4, "entry": "03", "title": "03", "file_count": 0, "offset": 1.5},
		{"id": 6, "season_binding_id": null, "entry": null, "title": "手动上传", "file_count": 1, "offset": 0}
	]`)

	// 条目的位置按绑定 id
	assertRows(t, m.Pool, `
SELECT e.season_binding_id, e.label, e.position, f.name, encode(f.sha256, 'hex') AS sha256, f.size, convert_from(f.content, 'UTF8') AS content
FROM season_binding_entries e JOIN season_binding_entry_files f ON f.entry_id = e.id ORDER BY e.position, f.name`, `[
		{"season_binding_id": 4, "label": "04", "position": 1, "name": "04.xml", "sha256": "04", "size": 3, "content": "d04"},
		{"season_binding_id": 4, "label": "03", "position": 2, "name": "a.xml", "sha256": "0a", "size": 3, "content": "d3a"},
		{"season_binding_id": 4, "label": "03", "position": 2, "name": "b.xml", "sha256": "0b", "size": 3, "content": "d3b"}
	]`)
	assertRows(t, m.Pool, `SELECT binding_id, name FROM binding_files ORDER BY id`, `[
		{"binding_id": 6, "name": "manual.xml"}
	]`)

	// 删除季绑定连同它建出的绑定；槽位冲突时置空来源的绑定留下
	exec(t, m.Pool, `DELETE FROM season_bindings WHERE id IN (1, 4)`)
	assertRows(t, m.Pool, `SELECT id FROM bindings ORDER BY id`, `[{"id": 1}, {"id": 6}]`)
	assertRows(t, m.Pool, `SELECT count(*) AS n FROM season_binding_entry_files`, `[{"n": 0}]`)
}

func TestMigration00008Down(t *testing.T) {
	t.Parallel()
	m := dbtest.NewMigrator(t)
	m.UpTo(t, 7)
	seed00007(t, m.Pool)
	m.UpTo(t, 8)

	m.DownTo(t, 7)

	// 文件夹的季绑定丢掉规则和对应；finished 按默认值加回
	assertRows(t, m.Pool, `SELECT id, kind, finished, mapping_from, episode_patterns, updated_at IS NOT NULL AS has_updated_at FROM season_bindings ORDER BY id`, `[
		{"id": 1, "kind": "collection", "finished": false, "mapping_from": 1, "episode_patterns": ["(\\d+)"], "has_updated_at": true},
		{"id": 2, "kind": "collection", "finished": false, "mapping_from": 1, "episode_patterns": ["(\\d+)"], "has_updated_at": true},
		{"id": 3, "kind": "collection", "finished": false, "mapping_from": 1, "episode_patterns": ["(\\d+)"], "has_updated_at": true},
		{"id": 4, "kind": "folder", "finished": false, "mapping_from": null, "episode_patterns": null, "has_updated_at": true}
	]`)

	// 条目的文件移回绑定，重算份数，标题补回"F / "；scale 按默认值加回
	assertRows(t, m.Pool, `SELECT id, season_binding_id, title, file_count, scale FROM bindings ORDER BY id`, `[
		{"id": 1, "season_binding_id": null, "title": "旧稿", "file_count": 0, "scale": 1},
		{"id": 2, "season_binding_id": 1, "title": "第1话", "file_count": 0, "scale": 1},
		{"id": 3, "season_binding_id": 1, "title": "第2话", "file_count": 0, "scale": 1},
		{"id": 4, "season_binding_id": 4, "title": "F / 04", "file_count": 1, "scale": 1},
		{"id": 5, "season_binding_id": 4, "title": "F / 03", "file_count": 2, "scale": 1},
		{"id": 6, "season_binding_id": null, "title": "手动上传", "file_count": 1, "scale": 1}
	]`)
	assertRows(t, m.Pool, `
SELECT binding_id, name, encode(sha256, 'hex') AS sha256, size, convert_from(content, 'UTF8') AS content
FROM binding_files ORDER BY binding_id, name`, `[
		{"binding_id": 4, "name": "04.xml", "sha256": "04", "size": 3, "content": "d04"},
		{"binding_id": 5, "name": "a.xml", "sha256": "0a", "size": 3, "content": "d3a"},
		{"binding_id": 5, "name": "b.xml", "sha256": "0b", "size": 3, "content": "d3b"},
		{"binding_id": 6, "name": "manual.xml", "sha256": "06", "size": 3, "content": "dmm"}
	]`)

	// 处理过的记录由建出的链接绑定重建
	assertRows(t, m.Pool, `SELECT season_binding_id, ref, episode_id FROM season_binding_handled ORDER BY episode_id`, `[
		{"season_binding_id": 1, "ref": {"kind": "episode", "epId": 11}, "episode_id": 1},
		{"season_binding_id": 1, "ref": {"kind": "episode", "epId": 12}, "episode_id": 2}
	]`)

	// 删除季绑定时建出的绑定变回普通绑定
	exec(t, m.Pool, `DELETE FROM season_bindings WHERE id = 4`)
	assertRows(t, m.Pool, `SELECT id, season_binding_id FROM bindings WHERE id IN (4, 5) ORDER BY id`, `[
		{"id": 4, "season_binding_id": null},
		{"id": 5, "season_binding_id": null}
	]`)
}

// assertRows 把查询结果转成 JSON 数组（列名为键），按语义与 want 比较。
func assertRows(t *testing.T, pool *pgxpool.Pool, query, want string) {
	t.Helper()
	var got []byte
	if err := pool.QueryRow(t.Context(), `SELECT coalesce(json_agg(r), '[]') FROM (`+query+`) r`).Scan(&got); err != nil {
		t.Fatalf("query %s: %v", query, err)
	}
	testutil.AssertJSON(t, got, want)
}
