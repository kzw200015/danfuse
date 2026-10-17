-- +goose Up
-- 首播日期：剧第一次播出、电影上映的日期，只到日。同步时随目录源覆盖，不参与剧的身份；剧列表按它倒序排。
-- 已有的剧留空，下次同步补上。
ALTER TABLE series
    ADD COLUMN premiere_date DATE;

-- +goose Down
ALTER TABLE series
    DROP COLUMN premiere_date;
