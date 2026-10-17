-- name: UpdateSeriesByTMDBID :one
-- 按身份 (type, tmdb_id) 更新一部剧：标题、年份、原名、目录源 ID 都随目录源覆盖（见 docs/adr/0007）。没有这部剧时没有行。
-- 海报由同步核心随后按 sha256 处理，带回整行是为了剧现在的海报。
UPDATE series
SET title             = sqlc.arg(title),
    original_title    = sqlc.arg(original_title),
    year              = sqlc.arg(year),
    catalog_source_id = sqlc.arg(catalog_source_id),
    updated_at        = now()
WHERE type = sqlc.arg(type)
  AND tmdb_id = sqlc.arg(tmdb_id)::bigint
RETURNING *;

-- name: AdoptSeriesByNaturalKey :one
-- 按自然键 (type, title, year) 在没有 TMDB ID 的剧里找一部，覆盖原名、目录源 ID 并补上 TMDB ID（目录源没给时仍为空）。
-- 年份都为空也算同一部剧。没有这部剧时没有行。
UPDATE series
SET original_title    = sqlc.arg(original_title),
    tmdb_id           = sqlc.arg(tmdb_id),
    catalog_source_id = sqlc.arg(catalog_source_id),
    updated_at        = now()
WHERE type = sqlc.arg(type)
  AND title = sqlc.arg(title)
  AND year IS NOT DISTINCT FROM sqlc.narg(year)::int
  AND tmdb_id IS NULL
RETURNING *;

-- name: InsertSeries :one
INSERT INTO series (type, title, original_title, year, tmdb_id, catalog_source_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: SetSeriesPoster :exec
-- 剧指向新的海报，没有图时为 null。
UPDATE series
SET poster_image_id = $2
WHERE id = $1;

-- name: UpsertSeason :one
-- 按自然键 (series_id, number) 写入一季：匹配上就用目录源的数据覆盖键以外的字段，匹配不上就新增。
-- created 表示这一行是这次新增的：新插入的行 xmax 为 0，ON CONFLICT DO UPDATE 更新过的行不为 0。
INSERT INTO seasons (series_id, number, title)
VALUES ($1, $2, $3)
ON CONFLICT (series_id, number) DO UPDATE
SET title      = excluded.title,
    updated_at = now()
RETURNING id, (xmax = 0)::boolean AS created;

-- name: UpsertEpisodes :one
-- 一条语句写入一季的集，按自然键 (season_id, number) 匹配：匹配上且标题或时长变了就覆盖，没变的不重写；匹配不上就新增。
-- 返回新增的集数（新插入的行 xmax 为 0）。标题空串、时长 -1 存为 null。
-- 同一批里集号不能重复（ON CONFLICT DO UPDATE 不能在一条语句里改同一行两次），调用方先去重。
WITH written AS (
    INSERT INTO episodes (season_id, number, title, duration)
    SELECT @season_id::bigint,
           unnest(@numbers::int[]),
           nullif(unnest(@titles::text[]), ''),
           nullif(unnest(@durations::int[]), -1)
    ON CONFLICT (season_id, number) DO UPDATE
    SET title      = excluded.title,
        duration   = excluded.duration,
        updated_at = now()
    WHERE (episodes.title, episodes.duration) IS DISTINCT FROM (excluded.title, excluded.duration)
    RETURNING xmax = 0 AS created
)
SELECT count(*) FILTER (WHERE created)::int
FROM written;

-- name: SetSeasonSearchVectors :exec
-- 一条语句写入多季的搜索列，没变的不重写（列上有 GIN 索引，重写一行要重新插入它的全部词条）。
-- 搜索列是 Go 生成的 tsvector 文本（catalog.SearchVector），直接转换，不经过 PostgreSQL 的分词器。
UPDATE seasons se
SET search_vector = u.vector::tsvector
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@vectors::text[]) AS vector) u
WHERE se.id = u.id
  AND se.search_vector IS DISTINCT FROM u.vector::tsvector;
