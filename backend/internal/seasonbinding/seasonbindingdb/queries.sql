-- name: SeasonExists :one
-- 预览、创建季绑定前确认这一季存在，请求平台、解析文件之前就能返回 404。
SELECT EXISTS (SELECT 1 FROM seasons WHERE id = $1);

-- name: LockSeason :one
-- 写入事务的第一句：锁住这一季到提交，期间删不掉它。这一季已被删除时没有行。
SELECT id
FROM seasons
WHERE id = $1
FOR KEY SHARE;

-- name: LockSeasonBindingShared :one
-- 补建写入一个槽位的事务的第二句（第一句锁季）：锁住季绑定到提交，期间删不掉它；之后的删除要等这个事务提交，
-- 再连同刚建出的绑定一起删掉。FOR KEY SHARE 与开关追更、记录检查结果的 UPDATE 兼容。季绑定已被删除时没有行。
SELECT id
FROM season_bindings
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

-- name: SeasonBindingExists :one
-- 这一季是否已经绑定过这个合集（不算 exclude_id 这个季绑定自己）。只用于在列出合集前省掉一次注定 409 的请求，
-- 并发时以唯一约束为准。
SELECT EXISTS (SELECT 1
               FROM season_bindings
               WHERE season_id = @season_id
                 AND adapter = @adapter::text
                 AND ref = @ref::jsonb
                 AND id IS DISTINCT FROM sqlc.narg(exclude_id)::bigint);

-- name: InsertSeasonBinding :one
-- 合集的季绑定。同一季重复绑定同一个合集时撞上唯一约束 (season_id, adapter, ref)。
INSERT INTO season_bindings (season_id, kind, adapter, ref, link, title, mapping_from, mapping_to, episode_patterns,
                             numbered_by_rule)
VALUES (@season_id, 'collection', @adapter::text, @ref::jsonb, @link::text, @title, @mapping_from::int, @mapping_to::int,
        @episode_patterns::text[], @numbered_by_rule::boolean)
RETURNING id;

-- name: UpsertSeasonBindingItems :exec
-- 写入一次列出的结果，各数组按下标一一对应，ref 是 JSON 文本。number 是平台给的集号，为 -1 时存为 null
-- （按规则编号的合集、平台没给出整数集号的条目）。按 (季绑定, 弹幕源) upsert：已有的条目更新位置、集号与标签，
-- 上次失败的原因保留。调用方保证 ref 不重复。同 InsertDanmaku，SELECT 列表里的多个 unnest 同步展开。
INSERT INTO season_binding_items (season_binding_id, ref, position, number, label)
SELECT @season_binding_id::bigint,
       unnest(@refs::text[])::jsonb,
       unnest(@positions::int[]),
       nullif(unnest(@numbers::int[]), -1),
       unnest(@labels::text[])
ON CONFLICT (season_binding_id, ref) DO UPDATE
SET position = excluded.position,
    number   = excluded.number,
    label    = excluded.label;

-- name: DeleteStaleSeasonBindingItems :exec
-- 删掉合集里已经没有的条目。refs 是这次列出的全部 ref（JSON 文本），按 jsonb 比较。
DELETE FROM season_binding_items i
WHERE i.season_binding_id = @season_binding_id
  AND NOT EXISTS (SELECT 1 FROM unnest(@refs::text[]) AS r (ref) WHERE r.ref::jsonb = i.ref);

-- name: ListSeasonBindingItems :many
-- 合集上次列出的结果，按在合集里的顺序。
SELECT *
FROM season_binding_items
WHERE season_binding_id = $1
ORDER BY position;

-- name: RecordSeasonBindingListed :one
-- 一次列出成功：季绑定恢复为正常、清掉错误，更新合集标题与是否按集号规则编号；返回更新后的季绑定（表单），
-- 同一个事务里随后保存条目、派生。季绑定已被删除时没有行。
UPDATE season_bindings
SET status           = 'active',
    last_error       = NULL,
    title            = @title,
    numbered_by_rule = @numbered_by_rule::boolean
WHERE id = @id
RETURNING *;

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

-- name: SaveCollectionForm :exec
-- 保存合集的表单（调用方已锁住季绑定）：链接、选的合集、集号对应、集号规则。换成另一个合集（适配器或 ref 变了）时
-- 清掉上次检查时间，追更按从没检查过算。同一季已经绑定过这个合集时撞上唯一约束 (season_id, adapter, ref)。
-- 合集的标题、是否按规则编号和条目随后由 RecordSeasonBindingListed 与 UpsertSeasonBindingItems 写入。
UPDATE season_bindings
SET adapter          = @adapter::text,
    ref              = @ref::jsonb,
    link             = @link::text,
    mapping_from     = @mapping_from::int,
    mapping_to       = @mapping_to::int,
    episode_patterns = @episode_patterns::text[],
    last_checked_at  = CASE WHEN adapter = @adapter::text AND ref = @ref::jsonb THEN last_checked_at END
WHERE id = @id;

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

-- name: ListSeasonBindingEntries :many
-- 文件夹的季绑定保存的条目（不含文件），按位置排列。
SELECT id, label, position
FROM season_binding_entries
WHERE season_binding_id = $1
ORDER BY position;

-- name: InsertFolderSeasonBinding :one
-- 文件夹的季绑定，标题为文件夹名。只属于合集的列为空，追更恒为关。
INSERT INTO season_bindings (season_id, kind, title, follow, mapping_from, mapping_to, episode_patterns)
VALUES (@season_id, 'folder', @title, false, @mapping_from::int, @mapping_to::int, @episode_patterns::text[])
RETURNING *;

-- name: UpdateFolderSeasonBinding :one
-- 保存文件夹的季绑定的表单：标题（换文件夹时为新的文件夹名）、集号规则、集号对应。返回更新后的季绑定。
UPDATE season_bindings
SET title            = @title,
    mapping_from     = @mapping_from::int,
    mapping_to       = @mapping_to::int,
    episode_patterns = @episode_patterns::text[]
WHERE id = @id
RETURNING *;

-- name: ListSeasonBindingEntryHashes :many
-- 文件夹的季绑定的各条目的文件哈希，换文件夹时按"名称 + 文件哈希的集合"对上保存的条目。
SELECT f.entry_id, f.sha256
FROM season_binding_entry_files f
JOIN season_binding_entries en ON en.id = f.entry_id
WHERE en.season_binding_id = $1;

-- name: InsertSeasonBindingEntry :one
-- 文件夹的一个条目，名称不带文件夹名。
INSERT INTO season_binding_entries (season_binding_id, label, position)
VALUES (@season_binding_id, @label, @position)
RETURNING id;

-- name: InsertSeasonBindingEntryFiles :exec
-- 条目的弹幕文件，各数组按下标一一对应，按加入的顺序。条目里内容相同的文件只存一份。
-- 同 InsertDanmaku，SELECT 列表里的多个 unnest 同步展开。
INSERT INTO season_binding_entry_files (entry_id, name, sha256, size, content)
SELECT @entry_id::bigint,
       unnest(@names::text[]),
       unnest(@sha256s::bytea[]),
       unnest(@sizes::int[]),
       unnest(@contents::bytea[])
ON CONFLICT (entry_id, sha256) DO NOTHING;

-- name: SetSeasonBindingEntryPosition :exec
-- 换文件夹时沿用的条目，位置改为在新文件夹里的。
UPDATE season_binding_entries
SET position = @position
WHERE id = @id;

-- name: DeleteSeasonBindingEntries :exec
-- 换文件夹时删掉没对上的条目，条目的文件随外键级联删除；它们建出的绑定由调用方事先删掉（DeleteSlotsInTx）。
DELETE FROM season_binding_entries
WHERE id = ANY (@ids::bigint[]);

-- name: ListSeasonBindingEntryFiles :many
-- 条目的弹幕文件连同内容，按加入的顺序，建出文件绑定时解析。
SELECT name, content
FROM season_binding_entry_files
WHERE entry_id = $1
ORDER BY id;
