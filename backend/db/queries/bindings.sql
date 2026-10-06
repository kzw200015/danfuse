-- name: EpisodeExists :one
-- 创建绑定前确认这一集存在，拉取之前就能返回 404。
SELECT EXISTS (SELECT 1 FROM episodes WHERE id = $1);

-- name: LockEpisode :one
-- 锁住这一集到提交，期间删不掉它：创建绑定的写入事务的第一句；补建的写入事务在锁住季、季绑定之后也用它锁集。
-- FOR KEY SHARE 与同步的 upsert 兼容。这一集已被删除时没有行。
SELECT id
FROM episodes
WHERE id = $1
FOR KEY SHARE;

-- name: BindingExists :one
-- 这一集是否已经绑定过这个弹幕源。只用于在拉取前省掉一次注定 409 的拉取，并发时以唯一约束为准。
SELECT EXISTS (SELECT 1 FROM bindings WHERE episode_id = @episode_id AND adapter = @adapter::text AND ref = @ref::jsonb);

-- name: InsertBinding :one
-- 贴链接建出的绑定。同一集重复绑定同一个弹幕源时撞上唯一约束 (episode_id, adapter, ref)。
INSERT INTO bindings (episode_id, kind, adapter, ref, title, duration)
VALUES (@episode_id, 'link', @adapter::text, @ref::jsonb, @title, @duration::int)
RETURNING id;

-- name: InsertFileBinding :one
-- 用弹幕文件建出的绑定：没有适配器、ref 和时长，弹幕文件随后在同一个事务里加入。
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

-- name: GetBindingFileContent :one
-- 重新解析时逐份读出文件内容，不一次读进全部文件。
SELECT content
FROM binding_files
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

-- name: InsertDanmaku :execrows
-- 写入一次拉取的弹幕，五个等长的数组按下标一一对应（SELECT 列表里的多个 unnest 同步展开）。
-- 按主键 (binding_id, source_id) 去重：已有的、以及同一批里重复的原始 ID 都跳过，返回实际插入的条数。
INSERT INTO danmaku (binding_id, source_id, time_ms, mode, color, text)
SELECT @binding_id::bigint,
       unnest(@source_ids::bigint[]),
       unnest(@time_ms::int[]),
       unnest(@modes::smallint[]),
       unnest(@colors::int[]),
       unnest(@texts::text[])
ON CONFLICT DO NOTHING;

-- name: DeleteDanmaku :exec
-- 清空后重新拉取：在写入这次结果的同一个事务里，先删掉这个绑定的全部弹幕。
DELETE FROM danmaku
WHERE binding_id = $1;

-- name: RecordDanmaku :one
-- 写入弹幕之后更新绑定的计数（拉取、追加文件、重新解析共用）：
--   只增不删时，新增条数计入 danmaku_count，插入了新弹幕时 content_version 加 1；
--   替换（清空后重新拉取、重新解析）时，danmaku_count 设为这次插入的条数，content_version 不论插入几条都加 1。
UPDATE bindings
SET danmaku_count   = CASE WHEN @replace::boolean THEN 0 ELSE danmaku_count END + @added::int,
    content_version = content_version + (@replace::boolean OR @added::int > 0)::int,
    updated_at      = now()
WHERE id = @id
RETURNING *;

-- name: RecordFetch :exec
-- 一次拉取成功后更新弹幕源的信息：标题、时长用这次拉取的值覆盖，拉取成功即为 active。
-- 只更新拉取相关的列，不覆盖 offset；计数由 RecordDanmaku 维护。
-- 拉取时间由应用写入：追更按它判断自动重新拉取是否已满 24 小时，与上次检查时间用同一个时钟。
UPDATE bindings
SET title           = @title,
    duration        = @duration::int,
    status          = 'active',
    last_fetched_at = @fetched_at::timestamptz,
    updated_at      = now()
WHERE id = @id;

-- name: AddBindingFileCount :exec
-- 追加文件后把新加入的份数计入 file_count，与文件在同一个事务里。
UPDATE bindings
SET file_count = file_count + @files::int,
    updated_at = now()
WHERE id = @id;

-- name: MarkBindingDead :exec
-- 重新拉取时弹幕源已不存在：标为失效。已保存的弹幕、计数、标题和时长都不动；
-- 这次拉取得到了确定的结果，拉取时间照常更新，由应用写入（同 RecordFetch）。
UPDATE bindings
SET status          = 'dead',
    last_fetched_at = sqlc.arg(fetched_at)::timestamptz,
    updated_at      = now()
WHERE id = sqlc.arg(id);

-- name: UpdateBindingOffset :one
-- 只改偏移，content_version 不变。
UPDATE bindings
SET "offset"   = $2,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- name: DeleteBinding :execrows
-- 删除绑定，它的弹幕随外键级联删除。
DELETE FROM bindings
WHERE id = $1;

-- name: ListBindingsBySeries :many
-- 剧详情用：一部剧所有集的绑定，按创建顺序排列。
SELECT b.*
FROM bindings b
JOIN episodes e ON e.id = b.episode_id
JOIN seasons se ON se.id = e.season_id
WHERE se.series_id = $1
ORDER BY b.id;
