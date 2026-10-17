-- name: LockEpisode :one
-- 锁住这一集到提交，期间删不掉它：创建绑定的写入事务的第一句；季绑定写入槽位时在锁住季、季绑定之后也用它锁集。
-- FOR KEY SHARE 与同步的 upsert 兼容。这一集已被删除时没有行。
SELECT id
FROM episodes
WHERE id = $1
FOR KEY SHARE;

-- name: CountEntryFiles :one
-- 条目的弹幕文件份数：季绑定建出的文件绑定的标签里写明份数。
SELECT count(*)::int
FROM season_binding_entry_files
WHERE entry_id = $1;

-- name: GetSeasonBindingTitle :one
-- 建出的绑定不能单独删除、追加文件，提示里写明建出它的季绑定。
SELECT title
FROM season_bindings
WHERE id = $1;

-- name: GetBinding :one
-- 重新拉取之前取出适配器和 ref；追加文件、重新解析之前确认是用弹幕文件建的。
SELECT *
FROM bindings
WHERE id = $1;

-- name: LockBinding :one
-- 重新拉取、标为失效、追加文件、重新解析的写入事务的第一句：锁住这个绑定到提交。同一个绑定的写入因此排队执行，
-- 计数的算术准确；删除绑定也要等它提交。绑定已被删除时没有行。
SELECT id
FROM bindings
WHERE id = $1
FOR UPDATE;

-- name: UpdateBindingOffset :one
-- 只改偏移，content_version 不变。
UPDATE bindings
SET "offset" = $2
WHERE id = $1
RETURNING *;

-- name: DeleteBinding :execrows
-- 删除绑定，它的弹幕随外键级联删除。季绑定建出的绑定不删，没有行（随季绑定的表单增删）。
DELETE FROM bindings
WHERE id = $1
  AND season_binding_id IS NULL;

-- name: ListBindingsBySeries :many
-- 剧详情用：一部剧所有集的绑定，按创建顺序排列；季绑定建出的文件绑定带上条目的文件份数（其余为 0）。
SELECT sqlc.embed(b),
       (SELECT count(*) FROM season_binding_entry_files f WHERE f.entry_id = b.entry_id)::int AS entry_file_count
FROM bindings b
JOIN episodes e ON e.id = b.episode_id
JOIN seasons se ON se.id = e.season_id
WHERE se.series_id = $1
ORDER BY b.id;
