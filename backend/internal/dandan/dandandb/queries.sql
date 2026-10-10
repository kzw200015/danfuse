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
-- 搜索结果、作品详情里各季的集，按季、集号排序，每行带着这一季的总集数。
-- 给了 number 时只要这个集号的集（按集号搜索、识别），总集数仍是全部的集。
SELECT id, season_id, number, title, episode_count
FROM (SELECT id, season_id, number, title, count(*) OVER (PARTITION BY season_id) AS episode_count
      FROM episodes
      WHERE season_id = ANY (sqlc.arg(season_ids)::bigint[])) e
WHERE sqlc.narg(number)::int IS NULL OR number = sqlc.narg(number)::int
ORDER BY season_id, number;
