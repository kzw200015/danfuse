-- +goose Up
-- 最晚一条弹幕的时间（毫秒，未校正），管理界面查看弹幕时作为拖动条的长度。与 danmaku_count 一样在写入弹幕的事务里维护，
-- 读取时不 MAX；没有弹幕、或弹幕都在 0 之前时为 0。已有的绑定按现有的弹幕算出。
ALTER TABLE bindings
    ADD COLUMN max_time_ms INT NOT NULL DEFAULT 0;

UPDATE bindings b
SET max_time_ms = GREATEST(0, (SELECT max(time_ms) FROM danmaku d WHERE d.binding_id = b.id));

-- +goose Down
ALTER TABLE bindings
    DROP COLUMN max_time_ms;
