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
--   只增不删时，新增条数计入 danmaku_count，插入了新弹幕时 content_version 加 1，max_time_ms 取与这批最晚时间中较大的；
--   替换（清空后重新拉取、重新解析）时，danmaku_count 设为这次插入的条数，content_version 不论插入几条都加 1，
--   max_time_ms 设为这批的最晚时间。latest_ms 是这次写入的整批弹幕（含已有、跳过的）的最晚时间，不早于 0。
UPDATE bindings
SET danmaku_count   = CASE WHEN @replace::boolean THEN 0 ELSE danmaku_count END + @added::int,
    content_version = content_version + (@replace::boolean OR @added::int > 0)::int,
    max_time_ms     = GREATEST(CASE WHEN @replace::boolean THEN 0 ELSE max_time_ms END, @latest_ms::int)
WHERE id = @id
RETURNING *;

-- name: RecordFetch :exec
-- 一次拉取成功后更新弹幕源的信息：标题、时长用这次拉取的值覆盖，拉取成功即为 active。
-- 只更新拉取相关的列，不覆盖 offset；计数由 RecordDanmaku 维护。
-- 拉取时间由应用写入，同时也是上次尝试拉取的时间：定时拉取按它判断是否到期，与建出时间用同一个时钟。
UPDATE bindings
SET title              = @title,
    duration           = @duration::int,
    status             = 'active',
    last_fetched_at    = @fetched_at::timestamptz,
    fetch_attempted_at = @fetched_at::timestamptz
WHERE id = @id;
