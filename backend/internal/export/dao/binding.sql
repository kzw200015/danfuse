-- name: GetBinding :one
-- 导出一个绑定：文件名用的标题与校正用的偏移。绑定不存在时没有行。
SELECT title, "offset"
FROM bindings
WHERE id = $1;

-- name: ListDanmakuByBinding :many
-- 一个绑定保存的全部弹幕，不排序：要按校正后的时间排，由调用方校正之后再排。
SELECT source_id, time_ms, mode, color, text
FROM danmaku
WHERE binding_id = $1;
