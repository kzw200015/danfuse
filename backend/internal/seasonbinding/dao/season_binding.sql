-- name: SeasonExists :one
-- 预览、创建季绑定前确认这一季存在，请求平台、解析文件之前就能返回 404。
SELECT EXISTS (SELECT 1 FROM seasons WHERE id = $1);

-- name: LockSeason :one
-- 写入事务的第一句：锁住这一季到提交，期间删不掉它。这一季已被删除时没有行。
SELECT id
FROM seasons
WHERE id = $1
FOR KEY SHARE;

-- name: LockSeasonBindingForUpdate :one
-- 保存表单、删除季绑定、文件夹的写入事务里锁季之后锁住季绑定（FOR UPDATE）：与进行中的补建写入、另一次写入排队。季绑定已被删除时没有行。
SELECT *
FROM season_bindings
WHERE id = $1
FOR UPDATE;

-- name: GetSeasonBinding :one
-- 季绑定本身。不存在时没有行。
SELECT *
FROM season_bindings
WHERE id = $1;

-- name: GetSeasonBindingSummary :one
-- 季绑定的 JSON：连同它建出的、现存的绑定数，文件夹的条目数（合集为 0），以及是否正在补建
-- （有没有没过期的、按季绑定的租约，见 database.LeaseSeasonBackfill，lease_prefix 为它的前缀）。不存在时没有行。
SELECT sqlc.embed(sb),
       (SELECT count(*) FROM bindings b WHERE b.season_binding_id = sb.id)::int              AS binding_count,
       (SELECT count(*) FROM season_binding_entries en WHERE en.season_binding_id = sb.id)::int AS entry_count,
       EXISTS (SELECT 1
               FROM leases l
               WHERE l.key = sqlc.arg(lease_prefix)::text || sb.id
                 AND l.expires_at > now())::boolean                                          AS running
FROM season_bindings sb
WHERE sb.id = sqlc.arg(id);

-- name: ListSeasonBindingSummariesBySeries :many
-- 剧详情用：一部剧所有季的季绑定，列与 GetSeasonBindingSummary 相同，按创建顺序排列。
SELECT sqlc.embed(sb),
       (SELECT count(*) FROM bindings b WHERE b.season_binding_id = sb.id)::int              AS binding_count,
       (SELECT count(*) FROM season_binding_entries en WHERE en.season_binding_id = sb.id)::int AS entry_count,
       EXISTS (SELECT 1
               FROM leases l
               WHERE l.key = sqlc.arg(lease_prefix)::text || sb.id
                 AND l.expires_at > now())::boolean                                          AS running
FROM season_bindings sb
JOIN seasons se ON se.id = sb.season_id
WHERE se.series_id = sqlc.arg(series_id)
ORDER BY sb.id;

-- name: SetSeasonBindingFollow :execrows
-- 开关追更（只有合集的季绑定有追更）。不存在、或不是合集的季绑定时没有行。
UPDATE season_bindings
SET follow = @follow
WHERE id = @id
  AND kind = 'collection';

-- name: DeleteSeasonBinding :exec
-- 删除季绑定：它建出的绑定、条目表、条目和条目的文件随外键级联删除。
DELETE FROM season_bindings
WHERE id = $1;
