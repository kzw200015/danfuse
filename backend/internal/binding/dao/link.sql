-- name: EpisodeExists :one
-- 创建绑定前确认这一集存在，拉取之前就能返回 404。
SELECT EXISTS (SELECT 1 FROM episodes WHERE id = $1);

-- name: BindingExists :one
-- 这一集是否已经绑定过这个弹幕源。只用于在拉取前省掉一次注定 409 的拉取，并发时以唯一约束为准。
SELECT EXISTS (SELECT 1 FROM bindings WHERE episode_id = @episode_id AND adapter = @adapter::text AND ref = @ref::jsonb);

-- name: InsertLinkBinding :one
-- 贴链接建的、或季绑定建出的链接绑定，建出的带上它的季绑定；建出时间由应用写入（定时拉取按它算窗口）。
-- 这一集已有同一个弹幕源的绑定时（唯一约束 (episode_id, adapter, ref)）什么都不做，没有行；
-- 撞上槽位约束 (season_binding_id, episode_id) 时报错（调用方先删掉旧的槽位，撞上是程序错误）。
INSERT INTO bindings (episode_id, kind, adapter, ref, title, duration, season_binding_id, created_at)
VALUES (@episode_id, 'link', @adapter::text, @ref::jsonb, @title, @duration::int, sqlc.narg(season_binding_id)::bigint, @created_at)
ON CONFLICT (episode_id, adapter, ref) DO NOTHING
RETURNING id;

-- name: MarkBindingDead :exec
-- 重新拉取时弹幕源已不存在：标为失效。已保存的弹幕、计数、标题和时长都不动；
-- 这次拉取得到了确定的结果，拉取时间与上次尝试拉取的时间照常更新，由应用写入（同 RecordFetch）。
UPDATE bindings
SET status             = 'dead',
    last_fetched_at    = sqlc.arg(fetched_at)::timestamptz,
    fetch_attempted_at = sqlc.arg(fetched_at)::timestamptz
WHERE id = sqlc.arg(id);

-- name: RecordFetchAttempt :exec
-- 拉取失败（弹幕源不存在之外的上游错误、限流）：只记下尝试拉取的时间，定时拉取等满一个间隔再试。
-- 单条语句，不锁绑定；绑定已被删除时什么都不做。
UPDATE bindings
SET fetch_attempted_at = sqlc.arg(attempted_at)::timestamptz
WHERE id = sqlc.arg(id);

-- name: ListDueScheduledFetches :many
-- 定时拉取到期的绑定：能重新拉取的、建出时间晚于 created_after（现在 - 窗口）、上次尝试拉取不晚于 due_before（现在 - 间隔）的，
-- 按上次尝试拉取的时间从早到晚。时间规则由调用方按配置传入。
-- id 不为空时只看这一个绑定：拉取它之前再确认一次仍然到期。
SELECT id
FROM bindings
WHERE kind = 'link'
  AND created_at > sqlc.arg(created_after)::timestamptz
  AND (fetch_attempted_at IS NULL OR fetch_attempted_at <= sqlc.arg(due_before)::timestamptz)
  AND (sqlc.narg(id)::bigint IS NULL OR id = sqlc.narg(id)::bigint)
ORDER BY fetch_attempted_at NULLS FIRST, id;
