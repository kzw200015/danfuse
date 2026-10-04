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
