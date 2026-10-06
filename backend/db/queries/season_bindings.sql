-- name: SeasonExists :one
-- 预览、创建季绑定前确认这一季存在，列出合集之前就能返回 404。
SELECT EXISTS (SELECT 1 FROM seasons WHERE id = $1);

-- name: ListEpisodeNumbersBySeason :many
-- 一季的全部集号：预览给出默认的集号对应，详情判断条目是否在等待对应的集。
SELECT number
FROM episodes
WHERE season_id = $1
ORDER BY number;

-- name: LockSeason :one
-- 创建季绑定、补建的写入事务的第一句：锁住这一季到提交，期间删不掉它。这一季已被删除时没有行。
SELECT id
FROM seasons
WHERE id = $1
FOR KEY SHARE;

-- name: SeasonBindingExists :one
-- 这一季是否已经绑定过这个合集。只用于在列出合集前省掉一次注定 409 的请求，并发时以唯一约束为准。
SELECT EXISTS (SELECT 1 FROM season_bindings WHERE season_id = $1 AND adapter = $2 AND ref = $3);

-- name: InsertSeasonBinding :one
-- 同一季重复绑定同一个合集时撞上唯一约束 (season_id, adapter, ref)。
INSERT INTO season_bindings (season_id, adapter, ref, title, finished, mapping_from, mapping_to, episode_patterns, numbered_by_rule)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING id;

-- name: UpsertSeasonBindingItems :exec
-- 写入一次检查列出的条目（或改集号规则之后重新认出的序号），各数组按下标一一对应，ref 是 JSON 文本。
-- 序号为 -1、原因为空串时存为 null。按 (季绑定, 弹幕源) upsert：已有的条目更新位置、序号与标签，上次失败的原因保留。
-- 调用方保证 ref 不重复。同 InsertDanmaku，SELECT 列表里的多个 unnest 同步展开。
INSERT INTO season_binding_items (season_binding_id, ref, position, number, unmatched_reason, label)
SELECT @season_binding_id::bigint,
       unnest(@refs::text[])::jsonb,
       unnest(@positions::int[]),
       nullif(unnest(@numbers::int[]), -1),
       nullif(unnest(@reasons::text[]), ''),
       unnest(@labels::text[])
ON CONFLICT (season_binding_id, ref) DO UPDATE
SET position         = excluded.position,
    number           = excluded.number,
    unmatched_reason = excluded.unmatched_reason,
    label            = excluded.label;

-- name: DeleteStaleSeasonBindingItems :exec
-- 删掉合集里已经没有的条目。refs 是这次列出的全部 ref（JSON 文本），按 jsonb 比较。
DELETE FROM season_binding_items i
WHERE i.season_binding_id = @season_binding_id
  AND NOT EXISTS (SELECT 1 FROM unnest(@refs::text[]) AS r (ref) WHERE r.ref::jsonb = i.ref);

-- name: GetSeasonBinding :one
-- 季绑定本身：补建读它，立即补建前确认它存在。不存在时没有行。
SELECT *
FROM season_bindings
WHERE id = $1;

-- name: GetSeasonBindingSummary :one
-- 季绑定的 JSON：连同它建出的、现存的绑定数，以及是否正在补建（有没有没过期的、按季绑定的租约，见 database.LeaseSeasonBackfill，
-- lease_prefix 为它的前缀）。不存在时没有行。
SELECT sqlc.embed(sb),
       (SELECT count(*) FROM bindings b WHERE b.season_binding_id = sb.id)::int AS binding_count,
       EXISTS (SELECT 1
               FROM leases l
               WHERE l.key = sqlc.arg(lease_prefix)::text || sb.id
                 AND l.expires_at > now())::boolean AS running
FROM season_bindings sb
WHERE sb.id = sqlc.arg(id);

-- name: ListSeasonBindingSummariesBySeries :many
-- 剧详情用：一部剧所有季的季绑定，列与 GetSeasonBindingSummary 相同，按创建顺序排列。
SELECT sqlc.embed(sb),
       (SELECT count(*) FROM bindings b WHERE b.season_binding_id = sb.id)::int AS binding_count,
       EXISTS (SELECT 1
               FROM leases l
               WHERE l.key = sqlc.arg(lease_prefix)::text || sb.id
                 AND l.expires_at > now())::boolean AS running
FROM season_bindings sb
JOIN seasons se ON se.id = sb.season_id
WHERE se.series_id = sqlc.arg(series_id)
ORDER BY sb.id;

-- name: ListSeasonBindingItems :many
-- 上次检查时的条目，按在合集里的顺序。
SELECT *
FROM season_binding_items
WHERE season_binding_id = $1
ORDER BY position;

-- name: GetSeasonBindingItemNumber :one
-- 补建处理一个条目之前重新读它的序号：补建进行中改了集号规则时用新认出的序号。条目已经不在时没有行，对不上时为 null。
SELECT number
FROM season_binding_items
WHERE season_binding_id = $1
  AND ref = $2;

-- name: ListSeasonBindingHandled :many
-- 处理过的记录，连同对到的集号，以及那一集上现在还有没有这个弹幕源的绑定（不论是不是补建出来的）。
SELECT h.ref,
       e.number AS episode_number,
       EXISTS (SELECT 1 FROM bindings b WHERE b.episode_id = h.episode_id AND b.adapter = sb.adapter AND b.ref = h.ref) AS bound
FROM season_binding_handled h
JOIN season_bindings sb ON sb.id = h.season_binding_id
JOIN episodes e ON e.id = h.episode_id
WHERE h.season_binding_id = $1;

-- name: GetEpisodeIDByNumber :one
-- 补建时按集号找本季的集。没有这一集时没有行。
SELECT id
FROM episodes
WHERE season_id = $1
  AND number = $2;

-- name: LockSeasonBindingShared :one
-- 补建的写入事务的第二句（第一句锁季）：锁住季绑定到提交，期间删不掉它；之后的删除要等这个事务提交，再连同刚建出的绑定一起删掉。
-- FOR KEY SHARE 与改集号对应、开关追更的 UPDATE 兼容。季绑定已被删除时没有行。
SELECT id
FROM season_bindings
WHERE id = $1
FOR KEY SHARE;

-- name: LockSeasonBindingForDelete :one
-- 删除季绑定的事务的第一句：等进行中的补建写入事务提交，之后补建再也锁不到它。季绑定已被删除时没有行。
SELECT id
FROM season_bindings
WHERE id = $1
FOR UPDATE;

-- name: InsertBackfilledBinding :one
-- 补建出一个绑定，带上建出它的季绑定；建出时间由应用写入（追更按它算自动重新拉取的窗口）。
-- 这一集已有同一个弹幕源的绑定时什么都不做，没有行。
INSERT INTO bindings (episode_id, kind, adapter, ref, title, duration, season_binding_id, created_at)
VALUES (@episode_id, 'link', @adapter::text, @ref::jsonb, @title, @duration::int, @season_binding_id::bigint, @created_at)
ON CONFLICT (episode_id, adapter, ref) DO NOTHING
RETURNING id;

-- name: InsertSeasonBindingHandled :exec
-- 记一条处理过的记录；已经记过时什么都不做。
INSERT INTO season_binding_handled (season_binding_id, ref, episode_id)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: SetSeasonBindingItemError :exec
-- 条目补建失败：记下原因和时间，下次补建再试。error 为 null 时清掉（补建成功）。
UPDATE season_binding_items
SET last_error    = sqlc.narg(error),
    last_error_at = sqlc.narg(error_at)
WHERE season_binding_id = sqlc.arg(season_binding_id)
  AND ref = sqlc.arg(ref);

-- name: RecordSeasonBindingListed :one
-- 一次检查成功列出合集：季绑定恢复为正常、清掉错误，更新合集标题、完结标志与是否按集号规则编号。
-- 返回集号规则：同一个事务里随后按它认出序号、写入条目；行锁让改集号规则的事务排在前面或后面，不会用旧规则覆盖新规则认出的序号。
-- 季绑定已被删除时没有行。
UPDATE season_bindings
SET status           = 'active',
    last_error       = NULL,
    title            = $2,
    finished         = $3,
    numbered_by_rule = $4,
    updated_at       = now()
WHERE id = $1
RETURNING episode_patterns;

-- name: FinishSeasonBindingCheck :exec
-- 一次检查结束（正常结束，或因错误、限流结束）：上次检查时间写为这一轮的开始时间，记下结束时的错误（没有时为 null）。
-- dead 时标为失效（列出合集时合集已不存在）。
UPDATE season_bindings
SET last_checked_at = sqlc.arg(checked_at)::timestamptz,
    last_error      = sqlc.narg(error),
    status          = CASE WHEN sqlc.arg(dead)::boolean THEN 'dead' ELSE status END,
    updated_at      = now()
WHERE id = sqlc.arg(id);

-- name: UpdateSeasonBinding :one
-- 改集号对应、集号规则，开关追更：只改传了的字段。返回是否按集号规则编号：改了集号规则时，同一个事务里随后重新认出条目的序号。
-- 不存在时没有行。
UPDATE season_bindings
SET follow           = coalesce(sqlc.narg(follow), follow),
    mapping_from     = coalesce(sqlc.narg(mapping_from), mapping_from),
    mapping_to       = coalesce(sqlc.narg(mapping_to), mapping_to),
    episode_patterns = coalesce(sqlc.narg(episode_patterns)::text[], episode_patterns),
    updated_at       = now()
WHERE id = sqlc.arg(id)
RETURNING numbered_by_rule;

-- name: DeleteBindingsBySeasonBinding :exec
-- 删除季绑定时"一起删"：它建出的绑定，弹幕随外键级联删除。
DELETE FROM bindings
WHERE season_binding_id = sqlc.arg(season_binding_id)::bigint;

-- name: DeleteSeasonBinding :exec
-- 删除季绑定，条目与处理过的记录随外键级联删除，它建出的绑定的来源置空。
DELETE FROM season_bindings
WHERE id = $1;

-- name: ListDueSeasonBindings :many
-- 追更的扫描：追更开着、并且满足以下任一条件的季绑定，按上次检查时间从早到晚：
--   从没检查过；距上次检查已满 12 小时（due_before = 现在 - 12 小时）；这一季里有集的建出时间晚于上次检查时间；
--   它建出的、建出不到 14 天（created_after = 现在 - 14 天）的绑定里，有距上次拉取已满 12 小时（同样以 due_before 判断）、
--   而且是在上次检查开始之后才满 12 小时的（满 12 小时之前开始的那一轮已经试过拉取它，失败了等下一次定期的检查，不每分钟重试）。
-- 12 小时由调用方传入（check_interval_seconds），时间规则只写在 service 里。
-- id 不为空时只看这一个季绑定：扫描拿到它的租约之后再确认一次仍然到期。
SELECT sb.id
FROM season_bindings sb
WHERE sb.follow
  AND (sqlc.narg(id)::bigint IS NULL OR sb.id = sqlc.narg(id)::bigint)
  AND (sb.last_checked_at IS NULL
    OR sb.last_checked_at <= sqlc.arg(due_before)::timestamptz
    OR EXISTS (SELECT 1 FROM episodes e WHERE e.season_id = sb.season_id AND e.created_at > sb.last_checked_at)
    OR EXISTS (SELECT 1
               FROM bindings b
               WHERE b.season_binding_id = sb.id
                 AND b.created_at > sqlc.arg(created_after)::timestamptz
                 AND b.last_fetched_at <= sqlc.arg(due_before)::timestamptz
                 AND b.last_fetched_at > sb.last_checked_at - sqlc.arg(check_interval_seconds)::int * interval '1 second'))
ORDER BY sb.last_checked_at NULLS FIRST, sb.id;

-- name: ListRecentBackfilledBindings :many
-- 自动重新拉取的候选：季绑定建出的、建出时间晚于 created_after（现在 - 14 天）的绑定，按上次拉取时间从早到晚。
SELECT id, last_fetched_at
FROM bindings
WHERE season_binding_id = sqlc.arg(season_binding_id)::bigint
  AND created_at > sqlc.arg(created_after)::timestamptz
ORDER BY last_fetched_at NULLS FIRST, id;
