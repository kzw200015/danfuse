-- +goose Up
-- 目录：剧 → 季 → 集，同步按自然键 upsert。删除剧、季时级联删除下级。
CREATE TABLE series (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type           TEXT        NOT NULL CHECK (type IN ('tv', 'movie')),
    title          TEXT        NOT NULL,
    original_title TEXT,
    year           INT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 自然键：年份都为空也算同一部剧
    UNIQUE NULLS NOT DISTINCT (type, title, year)
);

CREATE TABLE seasons (
    id            BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    series_id     BIGINT      NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    number        INT         NOT NULL CHECK (number >= 0), -- 特别篇为第 0 季
    title         TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 搜索列：Go 生成带位置和权重的 tsvector 文本（catalog.SearchVector），不经过 PostgreSQL 的分词器。
    -- 同步写入一部剧时在同一个事务里重算它所有季的搜索列；分词规则或搜索列的组成改变时，新增一个 Go 迁移重算所有季。
    search_vector TSVECTOR    NOT NULL DEFAULT '',
    UNIQUE (series_id, number)
);

CREATE INDEX seasons_search_vector_idx ON seasons USING GIN (search_vector);

CREATE TABLE episodes (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    season_id  BIGINT      NOT NULL REFERENCES seasons (id) ON DELETE CASCADE,
    number     INT         NOT NULL CHECK (number >= 0),
    title      TEXT,
    duration   INT, -- 秒
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (season_id, number)
);

-- +goose Down
DROP TABLE episodes;
DROP TABLE seasons;
DROP TABLE series;
