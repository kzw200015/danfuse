-- +goose Up
-- 剧的身份（见 docs/adr/0007）：有 TMDB ID 的剧以 (type, tmdb_id) 为身份，标题和年份随目录源覆盖；
-- 没有的仍以自然键 (type, title, year) 为身份。TMDB 的电视剧和电影各有一套编号，所以类型参与唯一性。
-- 已有的剧留空，下次同步按自然键对上时补上。
ALTER TABLE series
    ADD COLUMN tmdb_id BIGINT CHECK (tmdb_id > 0),
    ADD CONSTRAINT series_type_tmdb_id_key UNIQUE (type, tmdb_id);

-- 自然键只约束没有 TMDB ID 的剧：有 TMDB ID 的剧标题、年份可以变，不再受自然键约束，
-- 两部 TMDB ID 不同、标题年份相同的剧也可以共存。
ALTER TABLE series
    DROP CONSTRAINT series_type_title_year_key;

CREATE UNIQUE INDEX series_natural_key_idx ON series (type, title, year) NULLS NOT DISTINCT
    WHERE tmdb_id IS NULL;

-- +goose Down
DROP INDEX series_natural_key_idx;

ALTER TABLE series
    DROP CONSTRAINT series_type_tmdb_id_key,
    DROP COLUMN tmdb_id,
    ADD CONSTRAINT series_type_title_year_key UNIQUE NULLS NOT DISTINCT (type, title, year);
