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
