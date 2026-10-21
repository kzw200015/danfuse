-- name: ListSeasonBindingEntries :many
-- 文件夹的季绑定保存的条目（不含文件），按位置排列。
SELECT id, label, position
FROM season_binding_entries
WHERE season_binding_id = $1
ORDER BY position;

-- name: InsertFolderSeasonBinding :one
-- 文件夹的季绑定，标题为文件夹名。只属于合集的列为空，追更恒为关。
INSERT INTO season_bindings (season_id, kind, title, follow, mapping_from, mapping_to, episode_rule)
VALUES (@season_id, 'folder', @title, false, @mapping_from::int, @mapping_to::int, @episode_rule)
RETURNING *;

-- name: UpdateFolderSeasonBinding :one
-- 保存文件夹的季绑定的表单：标题（换文件夹时为新的文件夹名）、集号规则、集号对应。返回更新后的季绑定。
UPDATE season_bindings
SET title            = @title,
    mapping_from     = @mapping_from::int,
    mapping_to       = @mapping_to::int,
    episode_rule = @episode_rule
WHERE id = @id
RETURNING *;

-- name: ListSeasonBindingEntryHashes :many
-- 文件夹的季绑定的各条目的文件哈希，换文件夹时按"名称 + 文件哈希的集合"对上保存的条目。
SELECT f.entry_id, f.sha256
FROM season_binding_entry_files f
JOIN season_binding_entries en ON en.id = f.entry_id
WHERE en.season_binding_id = $1;

-- name: InsertSeasonBindingEntries :many
-- 文件夹的条目，名称不带文件夹名，各数组按下标一一对应。带回 ID 与名称（一个季绑定里名称不重复），由调用方按名称对回。
-- 同 InsertDanmaku，SELECT 列表里的多个 unnest 同步展开。
INSERT INTO season_binding_entries (season_binding_id, label, position)
SELECT @season_binding_id::bigint,
       unnest(@labels::text[]),
       unnest(@positions::int[])
RETURNING id, label;

-- name: InsertSeasonBindingEntryFiles :exec
-- 条目的弹幕文件，各数组按下标一一对应，按加入的顺序。条目里内容相同的文件只存一份。
-- 同 InsertDanmaku，SELECT 列表里的多个 unnest 同步展开。
INSERT INTO season_binding_entry_files (entry_id, name, sha256, size, content)
SELECT unnest(@entry_ids::bigint[]),
       unnest(@names::text[]),
       unnest(@sha256s::bytea[]),
       unnest(@sizes::int[]),
       unnest(@contents::bytea[])
ON CONFLICT (entry_id, sha256) DO NOTHING;

-- name: SetSeasonBindingEntryPositions :exec
-- 换文件夹时沿用的条目，位置改为在新文件夹里的，两个数组按下标一一对应（同 InsertDanmaku，两个 unnest 同步展开）。
UPDATE season_binding_entries e
SET position = p.position
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@positions::int[]) AS position) AS p
WHERE e.id = p.id;

-- name: DeleteSeasonBindingEntries :exec
-- 换文件夹时删掉没对上的条目，条目的文件随外键级联删除；它们建出的绑定由调用方事先删掉（DeleteSlotsInTx）。
DELETE FROM season_binding_entries
WHERE id = ANY (@ids::bigint[]);

-- name: ListSeasonBindingEntryFiles :many
-- 一组条目的弹幕文件连同内容，按条目、加入的顺序，建出文件绑定时解析。
SELECT entry_id, name, content
FROM season_binding_entry_files
WHERE entry_id = ANY (@entry_ids::bigint[])
ORDER BY entry_id, id;
