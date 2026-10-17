-- name: ListSeasonEpisodeSlots :many
-- 派生的输入：一季的全部集，连同季绑定 season_binding_id 在各集的槽位（它建出的绑定，没有时各列为 null）。
-- season_binding_id 为 null（新建的预览）时都没有槽位。按集号排列。
SELECT e.id,
       e.number,
       b.id       AS binding_id,
       b.ref      AS binding_ref,
       b.entry_id AS binding_entry_id,
       b.title    AS binding_title,
       b.status   AS binding_status
FROM episodes e
LEFT JOIN bindings b ON b.episode_id = e.id AND b.season_binding_id = sqlc.narg(season_binding_id)::bigint
WHERE e.season_id = @season_id
ORDER BY e.number;

-- name: ListSeasonOccupiedSources :many
-- 派生的输入（只对合集）：一季各集上、不是季绑定 season_binding_id 建出的、适配器为 adapter 的绑定的弹幕源。
-- season_binding_id 为 null（新建的预览）时为全部绑定，手动建的也算。
SELECT e.number, b.ref
FROM episodes e
JOIN bindings b ON b.episode_id = e.id
WHERE e.season_id = @season_id
  AND b.adapter = @adapter::text
  AND (sqlc.narg(season_binding_id)::bigint IS NULL
    OR b.season_binding_id IS DISTINCT FROM sqlc.narg(season_binding_id)::bigint);

-- name: ListSeasonBindingItems :many
-- 合集上次列出的结果，按在合集里的顺序。
SELECT *
FROM season_binding_items
WHERE season_binding_id = $1
ORDER BY position;
