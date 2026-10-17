-- name: GetBinding :one
-- 导出一个绑定：文件名用的标题与校正用的偏移。绑定不存在时没有行。
SELECT title, "offset"
FROM bindings
WHERE id = $1;

-- name: ListDanmakuByBinding :many
-- 一个绑定保存的全部弹幕，按时间、同一时间按原始 ID。同一个绑定的弹幕校正时平移同一个偏移再四舍五入到毫秒，
-- 不改变先后，所以这也是校正后的顺序，调用方不再排序。
SELECT source_id, time_ms, mode, color, text
FROM danmaku
WHERE binding_id = $1
ORDER BY time_ms, source_id;
