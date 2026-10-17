-- name: LockEpisodes :many
-- 季绑定删除槽位时，在锁住季、季绑定之后按 ID 顺序锁住这些集到提交（随后可能在同一集上写入新的槽位），已被删除的集没有行。
SELECT id
FROM episodes
WHERE id = ANY(@ids::bigint[])
ORDER BY id
FOR KEY SHARE;

-- name: InsertEntryBinding :one
-- 文件夹的季绑定按条目建出的文件绑定：弹幕文件存在条目上（season_binding_entry_files），这里没有 binding_files，file_count 为 0。
-- 撞上槽位约束时报错（调用方先删掉旧的槽位）。
INSERT INTO bindings (episode_id, kind, title, season_binding_id, entry_id)
VALUES (@episode_id, 'file', @title, @season_binding_id::bigint, @entry_id::bigint)
RETURNING id;

-- name: DeleteSlots :exec
-- 删掉季绑定在这些集上的槽位（它建出的绑定），弹幕随外键级联删除。
DELETE FROM bindings
WHERE season_binding_id = @season_binding_id::bigint
  AND episode_id = ANY(@episode_ids::bigint[]);
