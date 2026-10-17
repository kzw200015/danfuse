-- name: GetSeason :one
-- 导出一季：zip 文件名与根目录用的剧名、季号。季不存在时没有行。
SELECT sr.title AS series_title, s.number AS season_number
FROM seasons s
JOIN series sr ON sr.id = s.series_id
WHERE s.id = $1;

-- name: ListEpisodesBySeason :many
-- 一季的全部集，按集号：zip 里的子目录按这个顺序写入。
SELECT id, number
FROM episodes
WHERE season_id = $1
ORDER BY number;
