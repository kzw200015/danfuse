-- +goose Up
-- 上次尝试拉取的时间：创建、手动重新拉取、定时拉取都写，成功失败都算。定时拉取按它判断是否到期：
-- 失败时拉取时间不变，靠它让失败的绑定也等满一个间隔再试。与绑定的建出时间、拉取时间一样由应用写入。
-- 用弹幕文件建的绑定没有。
-- 已有的绑定取上次拉取时间。
ALTER TABLE bindings
    ADD COLUMN fetch_attempted_at TIMESTAMPTZ,
    ADD CONSTRAINT bindings_file_fetch_attempted_check CHECK (kind = 'link' OR fetch_attempted_at IS NULL);

UPDATE bindings
SET fetch_attempted_at = last_fetched_at;

-- 定时拉取每分钟按建出时间列出窗口内的绑定
CREATE INDEX bindings_link_created_at_idx ON bindings (created_at) WHERE kind = 'link';

-- +goose Down
DROP INDEX bindings_link_created_at_idx;

ALTER TABLE bindings
    DROP COLUMN fetch_attempted_at;
