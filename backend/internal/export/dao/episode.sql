-- name: GetEpisode :one
-- 导出一集：zip 文件名用的剧名、剧的类型、季号与集号。集不存在时没有行。
SELECT sr.title AS series_title, sr.type AS series_type, s.number AS season_number, e.number AS episode_number
FROM episodes e
JOIN seasons s ON s.id = e.season_id
JOIN series sr ON sr.id = s.series_id
WHERE e.id = $1;

-- name: ListBindingsByEpisode :many
-- 一集有弹幕的绑定（失效的、季绑定建出的都在），按创建顺序：重名后缀按这个顺序编号。
-- 没有弹幕的绑定（danmaku_count 与弹幕在同一个事务里维护）不读，省掉读它弹幕的那次查询。
SELECT id, title, "offset"
FROM bindings
WHERE episode_id = $1 AND danmaku_count > 0
ORDER BY id;
