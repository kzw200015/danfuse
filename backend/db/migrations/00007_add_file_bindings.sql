-- +goose Up
-- 用弹幕文件建的绑定（见 docs/adr/0004）。kind 区分弹幕源的形态：link 是贴链接或补建出的，按适配器和 ref 能重新拉取；
-- file 是上传的一组弹幕文件，没有适配器和 ref（唯一约束 (episode_id, adapter, ref) 在 NULL 上不生效，同一集可以有多个），
-- 也没有视频时长，不会失效，不由季绑定建出。
ALTER TABLE bindings
    ADD COLUMN kind       TEXT NOT NULL DEFAULT 'link' CHECK (kind IN ('link', 'file')),
    ADD COLUMN file_count INT  NOT NULL DEFAULT 0, -- 弹幕文件的份数，在追加文件的事务里维护，读取时不 COUNT
    ALTER COLUMN adapter DROP NOT NULL,
    ALTER COLUMN ref DROP NOT NULL,
    ALTER COLUMN duration DROP NOT NULL,
    ADD CONSTRAINT bindings_kind_columns_check CHECK (
        (kind = 'link' AND adapter IS NOT NULL AND ref IS NOT NULL AND duration IS NOT NULL AND file_count = 0)
        OR (kind = 'file' AND adapter IS NULL AND ref IS NULL AND duration IS NULL AND status = 'active'
            AND last_fetched_at IS NULL AND season_binding_id IS NULL)
    );

-- 弹幕文件的原文件：解析规则改了以后，重新解析从这里读。删除绑定时级联删除。
-- 同一个绑定里按内容去重：追加已有的文件时跳过。
CREATE TABLE binding_files (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    binding_id  BIGINT      NOT NULL REFERENCES bindings (id) ON DELETE CASCADE,
    name        TEXT        NOT NULL, -- 上传时的文件名
    sha256      BYTEA       NOT NULL,
    size        INT         NOT NULL, -- 字节
    content     BYTEA       NOT NULL,
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (binding_id, sha256)
);

-- +goose Down
DROP TABLE binding_files;
DELETE FROM bindings WHERE kind = 'file';
ALTER TABLE bindings
    DROP CONSTRAINT bindings_kind_columns_check,
    ALTER COLUMN duration SET NOT NULL,
    ALTER COLUMN ref SET NOT NULL,
    ALTER COLUMN adapter SET NOT NULL,
    DROP COLUMN file_count,
    DROP COLUMN kind;
