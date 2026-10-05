-- name: SetSeasonSearchVector :exec
-- 搜索列是 Go 生成的 tsvector 文本（catalog.SearchVector），直接转换，不经过 PostgreSQL 的分词器。
UPDATE seasons
SET search_vector = sqlc.arg(search_vector)::text::tsvector
WHERE id = sqlc.arg(id);

-- name: SearchSeasons :many
-- 目录搜索，query 是 Go 拼好的 tsquery 文本（fulltext.Query）。排序全在这里：
-- ts_rank 降序（权重数组按 {D, C, B, A} 的顺序：A、B、C 为 1.0、0.67、0.33，D 不用）→ 剧名短的在前
-- → 年份降序，无年份最后 → 剧 id → 季号，特别篇最后。调用方多取一条，用来判断后面还有没有。
-- 给了 season 时只要这个季号的季，给了 episode 时只要有这个集号的季，都在截断之前过滤。
SELECT se.id, se.number, s.type, s.title, s.original_title, s.year
FROM seasons se
JOIN series s ON s.id = se.series_id
WHERE se.search_vector @@ sqlc.arg(query)::text::tsquery
  AND (sqlc.narg(season)::int IS NULL OR se.number = sqlc.narg(season)::int)
  AND (sqlc.narg(episode)::int IS NULL
    OR EXISTS (SELECT 1 FROM episodes e WHERE e.season_id = se.id AND e.number = sqlc.narg(episode)::int))
ORDER BY ts_rank('{0.1, 0.33, 0.67, 1.0}'::real[], se.search_vector, sqlc.arg(query)::text::tsquery) DESC,
         char_length(s.title),
         s.year DESC NULLS LAST,
         s.id,
         se.number = 0,
         se.number
LIMIT sqlc.arg(max_rows);

-- name: GetSeason :one
-- 弹弹 API 的作品详情：一季连同所属剧的类型、剧名、原名和年份，列与 SearchSeasons 相同。季不存在时没有行。
SELECT se.id, se.number, s.type, s.title, s.original_title, s.year
FROM seasons se
JOIN series s ON s.id = se.series_id
WHERE se.id = $1;

-- name: ListEpisodesBySeasons :many
-- 搜索结果、作品详情里各季的全部集，按季、集号排序。
SELECT id, season_id, number, title
FROM episodes
WHERE season_id = ANY (sqlc.arg(season_ids)::bigint[])
ORDER BY season_id, number;
