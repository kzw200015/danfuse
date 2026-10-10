-- name: ListBlockedWords :many
-- 全部屏蔽词，新加的在前。
SELECT id, kind, pattern, created_at
FROM blocked_words
ORDER BY id DESC;

-- name: CreateBlockedWord :one
-- 同类型、dedup_key 相同时违反唯一约束。
INSERT INTO blocked_words (kind, pattern, dedup_key)
VALUES ($1, $2, $3)
RETURNING id, kind, pattern, created_at;

-- name: DeleteBlockedWord :execrows
DELETE FROM blocked_words
WHERE id = $1;
