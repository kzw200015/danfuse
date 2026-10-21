-- name: InsertSeasonBinding :one
-- 合集的季绑定。同一季可以多次绑定同一个合集。
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

-- name: SaveCollectionForm :exec
-- 保存合集的表单（调用方已锁住季绑定）：链接、选的合集、集号对应、集号规则。换成另一个合集（适配器或 ref 变了）时
-- 清掉上次检查时间，追更按从没检查过算。
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
