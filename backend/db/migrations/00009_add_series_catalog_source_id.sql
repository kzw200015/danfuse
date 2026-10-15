-- +goose Up
-- 目录源 ID：剧在目录源里的编号（Jellyfin 的条目 Id），只用来生成跳回目录源的链接，不参与剧的身份（见 docs/adr/0007）。
-- 同步时随目录源覆盖，同一部剧在目录源里有多份时取最后写入的那份。已有的剧留空，下次同步补上。
ALTER TABLE series
    ADD COLUMN catalog_source_id TEXT;

-- +goose Down
ALTER TABLE series
    DROP COLUMN catalog_source_id;
