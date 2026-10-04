-- name: UpsertSeries :one
-- 按自然键 (type, title, year) 写入一部剧：匹配上就用目录源的数据覆盖键以外的字段，匹配不上就新增。
-- created 表示这一行是这次新增的：新插入的行 xmax 为 0，ON CONFLICT DO UPDATE 更新过的行不为 0。
INSERT INTO series (type, title, original_title, year)
VALUES ($1, $2, $3, $4)
ON CONFLICT (type, title, year) DO UPDATE
SET original_title = excluded.original_title,
    updated_at     = now()
RETURNING id, (xmax = 0)::boolean AS created;

-- name: UpsertSeason :one
-- 按自然键 (series_id, number) 写入一季，规则同 UpsertSeries。
INSERT INTO seasons (series_id, number, title)
VALUES ($1, $2, $3)
ON CONFLICT (series_id, number) DO UPDATE
SET title      = excluded.title,
    updated_at = now()
RETURNING id, (xmax = 0)::boolean AS created;

-- name: UpsertEpisode :one
-- 按自然键 (season_id, number) 写入一集，规则同 UpsertSeries。
INSERT INTO episodes (season_id, number, title, duration)
VALUES ($1, $2, $3, $4)
ON CONFLICT (season_id, number) DO UPDATE
SET title      = excluded.title,
    duration   = excluded.duration,
    updated_at = now()
RETURNING id, (xmax = 0)::boolean AS created;

-- name: ListSeries :many
-- 剧列表：全部剧连同季数、集数，一条 SQL 聚合。没有季、集的剧计为 0。
SELECT s.id, s.type, s.title, s.original_title, s.year,
       count(DISTINCT se.id)::int AS season_count,
       count(e.id)::int           AS episode_count
FROM series s
LEFT JOIN seasons se ON se.series_id = s.id
LEFT JOIN episodes e ON e.season_id = se.id
GROUP BY s.id
ORDER BY s.id;

-- name: GetSeries :one
SELECT *
FROM series
WHERE id = $1;

-- name: ListSeasonsBySeries :many
-- 只选剧详情用到的列，不取搜索列。
SELECT id, number, title
FROM seasons
WHERE series_id = $1
ORDER BY number;

-- name: ListEpisodesBySeries :many
-- 一部剧的全部集，按集号排序。
SELECT e.*
FROM episodes e
JOIN seasons se ON se.id = e.season_id
WHERE se.series_id = $1
ORDER BY e.number;
