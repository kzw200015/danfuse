-- name: EpisodeExists :one
-- 创建绑定前确认这一集存在，拉取之前就能返回 404。
SELECT EXISTS (SELECT 1 FROM episodes WHERE id = $1);

-- name: LockEpisode :one
-- 创建绑定的写入事务的第一句：锁住这一集到提交，期间删不掉它。FOR KEY SHARE 与同步的 upsert 兼容。
-- 这一集已被删除时没有行。
SELECT id
FROM episodes
WHERE id = $1
FOR KEY SHARE;

-- name: BindingExists :one
-- 这一集是否已经绑定过这个弹幕源。只用于在拉取前省掉一次注定 409 的拉取，并发时以唯一约束为准。
SELECT EXISTS (SELECT 1 FROM bindings WHERE episode_id = $1 AND adapter = $2 AND ref = $3);

-- name: InsertBinding :one
-- 同一集重复绑定同一个弹幕源时撞上唯一约束 (episode_id, adapter, ref)。
INSERT INTO bindings (episode_id, adapter, ref, title, duration)
VALUES ($1, $2, $3, $4, $5)
RETURNING id;

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

-- name: RecordFetch :one
-- 一次拉取写入弹幕之后更新绑定：新增条数计入 danmaku_count，插入了新弹幕时 content_version 加 1；
-- 标题、时长用这次拉取的值覆盖；拉取成功即为 active。只更新拉取相关的列，不覆盖 offset。
UPDATE bindings
SET danmaku_count   = danmaku_count + @added::int,
    content_version = content_version + (@added::int > 0)::int,
    title           = @title,
    duration        = @duration,
    status          = 'active',
    last_fetched_at = now(),
    updated_at      = now()
WHERE id = @id
RETURNING *;

-- name: ListBindingsBySeries :many
-- 剧详情用：一部剧所有集的绑定，按创建顺序排列。
SELECT b.*
FROM bindings b
JOIN episodes e ON e.id = b.episode_id
JOIN seasons se ON se.id = e.season_id
WHERE se.series_id = $1
ORDER BY b.id;
