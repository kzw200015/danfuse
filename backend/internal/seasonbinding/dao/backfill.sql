-- name: LockSeasonBindingShared :one
-- 补建写入一个槽位的事务的第二句（第一句锁季）：锁住季绑定到提交，期间删不掉它；之后的删除要等这个事务提交，
-- 再连同刚建出的绑定一起删掉。FOR KEY SHARE 与开关追更、记录检查结果的 UPDATE 兼容。季绑定已被删除时没有行。
SELECT id
FROM season_bindings
WHERE id = $1
FOR KEY SHARE;

-- name: SetSeasonBindingItemError :exec
-- 条目补建失败：记下原因和时间，下次补建再试。error 为 null 时清掉（补建成功、按已有同源绑定跳过）。
UPDATE season_binding_items
SET last_error    = sqlc.narg(error),
    last_error_at = sqlc.narg(error_at)
WHERE season_binding_id = sqlc.arg(season_binding_id)
  AND ref = sqlc.arg(ref);

-- name: FinishSeasonBindingCheck :exec
-- 一次检查结束（正常结束，或因错误、限流结束）：上次检查时间写为这一轮的开始时间，记下结束时的错误（没有时为 null）。
-- dead 时标为失效（列出合集时合集已不存在）。
UPDATE season_bindings
SET last_checked_at = sqlc.arg(checked_at)::timestamptz,
    last_error      = sqlc.narg(error),
    status          = CASE WHEN sqlc.arg(dead)::boolean THEN 'dead' ELSE status END
WHERE id = sqlc.arg(id);

-- name: ListDueSeasonBindings :many
-- 追更的扫描：追更开着、并且满足以下任一条件的季绑定，按上次检查时间从早到晚，每个条件各是一列（到期的原因，记进日志）：
--   never_checked 从没检查过；interval_due 距上次检查已满一个检查周期（due_before = 现在 - 检查周期）；
--   new_episodes 这一季里有集的建出时间晚于上次检查时间。
-- 检查周期由调用方按配置传入，时间规则只写在 service 里。文件夹的季绑定追更恒为关。
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
