-- name: UpsertSeries :one
-- 按自然键 (type, title, year) 写入一部剧：匹配上就用目录源的数据覆盖键以外的字段，匹配不上就新增。
-- created 表示这一行是这次新增的：新插入的行 xmax 为 0，ON CONFLICT DO UPDATE 更新过的行不为 0。
-- 海报由同步核心随后按 sha256 处理，这里带回剧现在的海报。
INSERT INTO series (type, title, original_title, year)
VALUES ($1, $2, $3, $4)
ON CONFLICT (type, title, year) DO UPDATE
SET original_title = excluded.original_title,
    updated_at     = now()
RETURNING id, (xmax = 0)::boolean AS created, poster_image_id;

-- name: SetSeriesPoster :exec
-- 剧指向新的海报，没有图时为 null。
UPDATE series
SET poster_image_id = $2
WHERE id = $1;

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
-- 剧列表：全部剧连同季数、集数和绑定统计，一条 SQL 聚合。没有季、集、绑定的计为 0。
-- 一集有多个绑定时连接出多行，所以季数、集数、已绑定集数都按 DISTINCT 计。
SELECT s.id, s.type, s.title, s.original_title, s.year, s.poster_image_id,
       count(DISTINCT se.id)::int                         AS season_count,
       count(DISTINCT e.id)::int                          AS episode_count,
       count(DISTINCT b.episode_id)::int                  AS bound_episode_count,
       count(b.id)::int                                   AS binding_count,
       count(b.id) FILTER (WHERE b.status = 'dead')::int AS dead_binding_count
FROM series s
LEFT JOIN seasons se ON se.series_id = s.id
LEFT JOIN episodes e ON e.season_id = se.id
LEFT JOIN bindings b ON b.episode_id = e.id
GROUP BY s.id
ORDER BY s.id;

-- name: GetSeries :one
SELECT *
FROM series
WHERE id = $1;

-- name: ListSeasonsBySeries :many
-- 剧详情、同步重算搜索列用：只选这几列，不取搜索列本身。
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

-- name: DeleteSeries :one
-- 删除一部剧，季、集、绑定和弹幕随外键级联删除。带回它的海报 ID，由调用方在同一个事务里删掉这张图（先删剧、再删图）。
-- 用 RETURNING 取，不先 SELECT：同步正在写这部剧时，删除等它的事务提交，再按最新的一行删除，取到的是同步刚换上的海报。
-- 剧不存在时没有行。
DELETE FROM series
WHERE id = $1
RETURNING poster_image_id;

-- name: DeleteSeason :execrows
-- 删除一季，集、绑定和弹幕随外键级联删除。
DELETE FROM seasons
WHERE id = $1;

-- name: DeleteEpisode :execrows
-- 删除一集，绑定和弹幕随外键级联删除。
DELETE FROM episodes
WHERE id = $1;
