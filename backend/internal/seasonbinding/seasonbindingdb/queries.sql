-- name: SeasonExists :one
-- 预览、创建季绑定前确认这一季存在，列出合集之前就能返回 404。
SELECT EXISTS (SELECT 1 FROM seasons WHERE id = $1);

-- name: ListEpisodeNumbersBySeason :many
-- 一季的全部集号：详情判断条目是否在等待对应的集。
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
SELECT EXISTS (SELECT 1 FROM season_bindings WHERE season_id = @season_id AND adapter = @adapter::text AND ref = @ref::jsonb);

-- name: InsertSeasonBinding :one
-- 合集的季绑定。同一季重复绑定同一个合集时撞上唯一约束 (season_id, adapter, ref)。
INSERT INTO season_bindings (season_id, kind, adapter, ref, title, finished, mapping_from, mapping_to, episode_patterns, numbered_by_rule)
VALUES (@season_id, 'collection', @adapter::text, @ref::jsonb, @title, @finished, @mapping_from::int, @mapping_to::int,
        @episode_patterns::text[], @numbered_by_rule::boolean)
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

-- name: ListSeasonBindingItemsWithHandled :many
-- 条目表：上次检查时的条目，按在合集里的顺序；处理过的（季绑定建出过绑定的弹幕源）带着绑定建在的那一集的集号，
-- 没处理过的为 null。ref 在这里按 jsonb 比较。
SELECT sqlc.embed(i), e.number AS handled_episode_number
FROM season_binding_items i
LEFT JOIN season_binding_handled h ON h.season_binding_id = i.season_binding_id AND h.ref = i.ref
LEFT JOIN episodes e ON e.id = h.episode_id
WHERE i.season_binding_id = $1
ORDER BY i.position;

-- name: ListUnhandledSeasonBindingItems :many
-- 补建要处理的条目：还没处理过的，按在合集里的顺序。ref 在这里按 jsonb 比较。
SELECT i.*
FROM season_binding_items i
WHERE i.season_binding_id = $1
  AND NOT EXISTS (SELECT 1
                  FROM season_binding_handled h
                  WHERE h.season_binding_id = i.season_binding_id
                    AND h.ref = i.ref)
ORDER BY i.position;

-- name: ListBoundSources :many
-- 季绑定的季里、它的适配器现有的全部绑定：在哪一集、哪个弹幕源、是不是它建出的。
-- 条目表据此分出已建绑定、集上已有（别人建的同一个弹幕源）和绑定已被删除。
SELECT e.number AS episode_number,
       b.ref,
       (b.season_binding_id IS NOT DISTINCT FROM sb.id)::boolean AS own
FROM season_bindings sb
JOIN episodes e ON e.season_id = sb.season_id
JOIN bindings b ON b.episode_id = e.id AND b.adapter = sb.adapter
WHERE sb.id = $1;

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

-- name: InsertSeasonBindingHandled :exec
-- 补建出一个绑定的同一个事务里记一条处理过的记录；已经记过时什么都不做。
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
    title            = @title,
    finished         = @finished,
    numbered_by_rule = @numbered_by_rule::boolean,
    updated_at       = now()
WHERE id = @id
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
-- 追更的扫描：追更开着、并且满足以下任一条件的季绑定，按上次检查时间从早到晚，每个条件各是一列（到期的原因，记进日志）：
--   never_checked 从没检查过；interval_due 距上次检查已满一个检查周期（due_before = 现在 - 检查周期）；
--   new_episodes 这一季里有集的建出时间晚于上次检查时间。
-- 检查周期由调用方按配置传入，时间规则只写在 service 里。
-- id 不为空时只看这一个季绑定：扫描拿到它的租约之后再确认一次仍然到期。
SELECT id, never_checked, interval_due, new_episodes
FROM (SELECT sb.id,
             sb.last_checked_at,
             (sb.last_checked_at IS NULL)::boolean AS never_checked,
             COALESCE(sb.last_checked_at <= sqlc.arg(due_before)::timestamptz, false)::boolean AS interval_due,
             EXISTS (SELECT 1 FROM episodes e WHERE e.season_id = sb.season_id AND e.created_at > sb.last_checked_at) AS new_episodes
      FROM season_bindings sb
      WHERE sb.follow
        AND (sqlc.narg(id)::bigint IS NULL OR sb.id = sqlc.narg(id)::bigint)) d
WHERE never_checked OR interval_due OR new_episodes
ORDER BY last_checked_at NULLS FIRST, id;
