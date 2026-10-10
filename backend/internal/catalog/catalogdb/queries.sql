-- name: UpdateSeriesByTMDBID :one
-- 按身份 (type, tmdb_id) 更新一部剧：标题、年份、原名都随目录源覆盖（见 docs/adr/0007）。没有这部剧时没有行。
-- 海报由同步核心随后按 sha256 处理，带回整行是为了剧现在的海报。
UPDATE series
SET title          = sqlc.arg(title),
    original_title = sqlc.arg(original_title),
    year           = sqlc.arg(year),
    updated_at     = now()
WHERE type = sqlc.arg(type)
  AND tmdb_id = sqlc.arg(tmdb_id)::bigint
RETURNING *;

-- name: AdoptSeriesByNaturalKey :one
-- 按自然键 (type, title, year) 在没有 TMDB ID 的剧里找一部，覆盖原名并补上 TMDB ID（目录源没给时仍为空）。
-- 年份都为空也算同一部剧。没有这部剧时没有行。
UPDATE series
SET original_title = sqlc.arg(original_title),
    tmdb_id        = sqlc.arg(tmdb_id),
    updated_at     = now()
WHERE type = sqlc.arg(type)
  AND title = sqlc.arg(title)
  AND year IS NOT DISTINCT FROM sqlc.narg(year)::int
  AND tmdb_id IS NULL
RETURNING *;

-- name: InsertSeries :one
INSERT INTO series (type, title, original_title, year, tmdb_id)
VALUES ($1, $2, $3, $4, $5)
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

-- name: ListSeries :many
-- 剧列表：全部剧连同季数、集数和绑定统计，一条 SQL 聚合。没有季、集、绑定的计为 0。
-- 一集有多个绑定时连接出多行，所以季数、集数、已绑定集数都按 DISTINCT 计。following 表示有开着追更的季绑定。
-- tmdb_id 用来在列表上标出没有 TMDB ID、按标题和年份对应的剧。
SELECT s.id, s.type, s.title, s.original_title, s.year, s.tmdb_id, s.poster_image_id,
       count(DISTINCT se.id)::int                         AS season_count,
       count(DISTINCT e.id)::int                          AS episode_count,
       count(DISTINCT b.episode_id)::int                  AS bound_episode_count,
       count(b.id)::int                                   AS binding_count,
       count(b.id) FILTER (WHERE b.status = 'dead')::int AS dead_binding_count,
       EXISTS (SELECT 1
               FROM season_bindings sb
               JOIN seasons fse ON fse.id = sb.season_id
               WHERE fse.series_id = s.id
                 AND sb.follow)                           AS following
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

-- name: InsertImage :one
INSERT INTO images (content_type, data, sha256)
VALUES ($1, $2, $3)
RETURNING id;

-- name: GetImage :one
-- 图片接口用：原始字节与 content-type。
SELECT content_type, data
FROM images
WHERE id = $1;

-- name: GetImageSHA256 :one
-- 同步时与目录源的新图比较，不取图片本身。
SELECT sha256
FROM images
WHERE id = $1;

-- name: DeleteImage :exec
DELETE FROM images
WHERE id = $1;

-- name: CreateSyncRun :one
-- 开始时间取自应用的时钟：定时同步按它算下一次的时间。
INSERT INTO sync_runs (trigger, status, started_at)
VALUES (sqlc.arg(trigger), 'running', sqlc.arg(started_at))
RETURNING id;

-- name: InterruptRunningSyncRuns :execrows
-- 把残留的 running（进程崩溃或被杀）改为 interrupted，结束时间未知，保持为空。
-- 只能在持有同步的租约时调用：这时不会有正在进行的同步。
UPDATE sync_runs
SET status = 'interrupted'
WHERE status = 'running';

-- name: DeleteOldSyncRuns :exec
-- 只保留最近 keep 次：新同步开始时先删到剩 19 次，再插入这一次。
DELETE FROM sync_runs
WHERE id NOT IN (SELECT id FROM sync_runs ORDER BY id DESC LIMIT sqlc.arg(keep));

-- name: UpdateSyncRun :exec
-- 写入一次同步的进度或最终状态；状态不再是 running 时记下结束时间。warnings 为 null 时不改（警告没有新增的进度）。
UPDATE sync_runs
SET status           = sqlc.arg(status),
    total            = sqlc.arg(total),
    done             = sqlc.arg(done),
    created_series   = sqlc.arg(created_series),
    created_seasons  = sqlc.arg(created_seasons),
    created_episodes = sqlc.arg(created_episodes),
    warnings         = coalesce(sqlc.narg(warnings), warnings),
    warning_count    = sqlc.arg(warning_count),
    error            = sqlc.arg(error),
    finished_at      = CASE WHEN sqlc.arg(status) = 'running' THEN NULL ELSE now() END
WHERE id = sqlc.arg(id);

-- name: ListSyncRuns :many
-- 列表不带警告正文。
SELECT id, trigger, status, started_at, finished_at, total, done,
       created_series, created_seasons, created_episodes, warning_count, error
FROM sync_runs
ORDER BY id DESC
LIMIT $1;

-- name: GetSyncRun :one
SELECT *
FROM sync_runs
WHERE id = $1;

-- name: SetSeasonSearchVectors :exec
-- 一条语句写入多季的搜索列，没变的不重写（列上有 GIN 索引，重写一行要重新插入它的全部词条）。
-- 搜索列是 Go 生成的 tsvector 文本（catalog.SearchVector），直接转换，不经过 PostgreSQL 的分词器。
UPDATE seasons se
SET search_vector = u.vector::tsvector
FROM (SELECT unnest(@ids::bigint[]) AS id, unnest(@vectors::text[]) AS vector) u
WHERE se.id = u.id
  AND se.search_vector IS DISTINCT FROM u.vector::tsvector;
