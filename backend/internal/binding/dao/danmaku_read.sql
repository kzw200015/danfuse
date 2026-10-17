-- name: ListBindingsByEpisode :many
-- 读取一集的弹幕用：这一集的全部绑定（含失效的），按创建顺序排列。集不存在时没有行。
SELECT *
FROM bindings
WHERE episode_id = $1
ORDER BY id;

-- name: ListDanmakuByBindings :many
-- 这些绑定落库的弹幕，时间未校正，带上所属的绑定。合并时重新排序，这里不排。
-- 一条 SELECT 读完：清空后重新拉取在一个事务里完成，每个绑定读到的要么全旧、要么全新。
SELECT binding_id, source_id, time_ms, mode, color, text
FROM danmaku
WHERE binding_id = ANY(@binding_ids::bigint[]);

-- name: ListBindingDanmakuPage :many
-- 管理界面查看一个绑定的弹幕：时间未校正，按 (time_ms, source_id) 升序，从游标之后取一页。
-- from_ms 不为 NULL 时只取这个时间及以后的（跳转）；两个游标参数都为 NULL 时从头取。
-- 按 binding_id 的主键过滤后在内存里排序，一个绑定的条数不大，不另建索引。
SELECT source_id, time_ms, mode, color, text
FROM danmaku
WHERE binding_id = @binding_id
  AND (sqlc.narg(from_ms)::int IS NULL OR time_ms >= sqlc.narg(from_ms)::int)
  AND (sqlc.narg(after_time_ms)::int IS NULL
       OR (time_ms, source_id) > (sqlc.narg(after_time_ms)::int, sqlc.narg(after_source_id)::bigint))
ORDER BY time_ms, source_id
LIMIT @page_limit;
