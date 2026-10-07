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
-- 写入一次同步的进度或最终状态；状态不再是 running 时记下结束时间。
UPDATE sync_runs
SET status           = sqlc.arg(status),
    total            = sqlc.arg(total),
    done             = sqlc.arg(done),
    created_series   = sqlc.arg(created_series),
    created_seasons  = sqlc.arg(created_seasons),
    created_episodes = sqlc.arg(created_episodes),
    warnings         = sqlc.arg(warnings),
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
