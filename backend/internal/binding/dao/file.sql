-- name: InsertFileBinding :one
-- 在集上用弹幕文件建出的绑定：没有适配器、ref 和时长，弹幕文件随后在同一个事务里加入。
INSERT INTO bindings (episode_id, kind, title)
VALUES (@episode_id, 'file', @title)
RETURNING id;

-- name: InsertBindingFile :execrows
-- 往绑定里加入一份弹幕文件。同一个绑定里已有内容相同的文件时什么都不做，返回 0。
INSERT INTO binding_files (binding_id, name, sha256, size, content)
VALUES (@binding_id, @name, @sha256, @size, @content)
ON CONFLICT (binding_id, sha256) DO NOTHING;

-- name: ListBindingFiles :many
-- 绑定的弹幕文件，不含内容，按加入的顺序排列。
SELECT id, name, size, uploaded_at
FROM binding_files
WHERE binding_id = $1
ORDER BY id;

-- name: ListEntryFiles :many
-- 季绑定建出的文件绑定的弹幕文件：它的条目的文件，不含内容，按存入的顺序排列。
SELECT f.id, f.name, f.size
FROM season_binding_entry_files f
JOIN bindings b ON b.entry_id = f.entry_id
WHERE b.id = $1
ORDER BY f.id;

-- name: GetEntryFileContent :one
-- 重新解析季绑定建出的文件绑定时逐份读出条目的文件内容。
SELECT content
FROM season_binding_entry_files
WHERE id = $1;

-- name: GetBindingFileContent :one
-- 重新解析时逐份读出文件内容，不一次读进全部文件。
SELECT content
FROM binding_files
WHERE id = $1;

-- name: AddBindingFileCount :exec
-- 追加文件后把新加入的份数计入 file_count，与文件在同一个事务里。
UPDATE bindings
SET file_count = file_count + @files::int
WHERE id = @id;
