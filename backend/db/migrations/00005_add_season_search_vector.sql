-- +goose Up
-- 季的搜索列：Go 生成带位置和权重的 tsvector 文本（catalog.SearchVector），不经过 PostgreSQL 的分词器。
-- 同步写入一部剧时在同一个事务里重算它所有季的搜索列；分词规则或搜索列的组成改变时，新增一个 Go 迁移重算所有季。
ALTER TABLE seasons ADD COLUMN search_vector TSVECTOR NOT NULL DEFAULT '';

CREATE INDEX seasons_search_vector_idx ON seasons USING GIN (search_vector);

-- +goose Down
ALTER TABLE seasons DROP COLUMN search_vector;
