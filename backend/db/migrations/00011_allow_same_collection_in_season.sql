-- +goose Up
-- 同一季可以多次绑定同一个合集（见 docs/adr/0011）：合集里装着几个来源季、本地合成一季时，每个来源季一个季绑定，
-- 各用自己的集号规则和集号对应。唯一约束原本也是 season_id 上唯一的索引，删除季时的级联和按季查询都靠它，换成普通索引。
ALTER TABLE season_bindings
    DROP CONSTRAINT season_bindings_season_id_adapter_ref_key;
CREATE INDEX season_bindings_season_id_idx ON season_bindings (season_id);

-- +goose Down
-- 同一季已经重复绑定了同一个合集时加不回约束，回退失败；先删掉多出来的季绑定再回退。
DROP INDEX season_bindings_season_id_idx;
ALTER TABLE season_bindings
    ADD CONSTRAINT season_bindings_season_id_adapter_ref_key UNIQUE (season_id, adapter, ref);
