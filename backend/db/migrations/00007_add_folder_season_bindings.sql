-- +goose Up
-- 季绑定分两种（见 docs/adr/0008）：collection 是合集的季绑定，按集号对应补建出链接绑定、可以追更；
-- folder 是按季上传留下的文件夹的季绑定，记着这次上传建出的文件绑定，名称（title）是所选的文件夹名，只能删除。
-- 只属于合集的季绑定的列改为可空，按 kind 守住：文件夹的季绑定没有适配器、合集、集号对应和集号规则，
-- 不追更、不会失效、不检查。唯一约束 (season_id, adapter, ref) 在 NULL 上不生效，只作用于合集的季绑定，同一个文件夹可以传多次。
-- 链接绑定只指向合集的季绑定、文件绑定只指向文件夹的季绑定，文件夹的季绑定没有条目和处理过的记录：这些跨表的规则由代码保证。
ALTER TABLE season_bindings
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'collection' CHECK (kind IN ('collection', 'folder')),
    ALTER COLUMN adapter DROP NOT NULL,
    ALTER COLUMN ref DROP NOT NULL,
    ALTER COLUMN mapping_from DROP NOT NULL,
    ALTER COLUMN mapping_to DROP NOT NULL,
    ALTER COLUMN episode_patterns DROP NOT NULL,
    ALTER COLUMN numbered_by_rule DROP NOT NULL,
    ADD CONSTRAINT season_bindings_kind_columns_check CHECK (
        (kind = 'collection' AND adapter IS NOT NULL AND ref IS NOT NULL AND mapping_from IS NOT NULL
            AND mapping_to IS NOT NULL AND episode_patterns IS NOT NULL AND numbered_by_rule IS NOT NULL)
        OR (kind = 'folder' AND adapter IS NULL AND ref IS NULL AND mapping_from IS NULL AND mapping_to IS NULL
            AND episode_patterns IS NULL AND numbered_by_rule IS NULL AND NOT follow AND NOT finished
            AND status = 'active' AND last_checked_at IS NULL AND last_error IS NULL)
    );

-- 文件绑定可以由文件夹的季绑定建出（带 season_binding_id）
ALTER TABLE bindings
    DROP CONSTRAINT bindings_kind_columns_check,
    ADD CONSTRAINT bindings_kind_columns_check CHECK (
        (kind = 'link' AND adapter IS NOT NULL AND ref IS NOT NULL AND duration IS NOT NULL AND file_count = 0)
        OR (kind = 'file' AND adapter IS NULL AND ref IS NULL AND duration IS NULL AND status = 'active'
            AND last_fetched_at IS NULL)
    );

-- +goose Down
-- 删掉文件夹的季绑定，它建出的文件绑定随外键置空来源，变回普通的文件绑定。
DELETE FROM season_bindings
WHERE kind = 'folder';

ALTER TABLE bindings
    DROP CONSTRAINT bindings_kind_columns_check,
    ADD CONSTRAINT bindings_kind_columns_check CHECK (
        (kind = 'link' AND adapter IS NOT NULL AND ref IS NOT NULL AND duration IS NOT NULL AND file_count = 0)
        OR (kind = 'file' AND adapter IS NULL AND ref IS NULL AND duration IS NULL AND status = 'active'
            AND last_fetched_at IS NULL AND season_binding_id IS NULL)
    );

ALTER TABLE season_bindings
    DROP CONSTRAINT season_bindings_kind_columns_check,
    DROP COLUMN kind,
    ALTER COLUMN adapter SET NOT NULL,
    ALTER COLUMN ref SET NOT NULL,
    ALTER COLUMN mapping_from SET NOT NULL,
    ALTER COLUMN mapping_to SET NOT NULL,
    ALTER COLUMN episode_patterns SET NOT NULL,
    ALTER COLUMN numbered_by_rule SET NOT NULL;
