-- +goose Up
-- 合集的季绑定记下创建（或换合集）时贴的链接，编辑时预填（见 docs/adr/0009）。只用来回显：合集以 ref 为准，
-- 同一个合集可以由不同的链接贴进来。这一列之前建的季绑定为空，编辑时由 ref 生成的合集链接代替；文件夹的季绑定没有链接。
ALTER TABLE season_bindings
    ADD COLUMN link TEXT,
    ADD CONSTRAINT season_bindings_link_check CHECK (kind = 'collection' OR link IS NULL);

-- +goose Down
ALTER TABLE season_bindings
    DROP CONSTRAINT season_bindings_link_check,
    DROP COLUMN link;
