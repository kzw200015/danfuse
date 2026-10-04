-- +goose Up
-- 绑定：一集与一个弹幕源的对应关系。删除集时级联删除它的绑定，删除绑定时级联删除它的弹幕。
CREATE TABLE bindings (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    episode_id      BIGINT           NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    adapter         TEXT             NOT NULL, -- 源适配器的 ID，一经发布不能再改
    ref             JSONB            NOT NULL, -- 弹幕源在平台内的引用，由适配器定义，适配器之外不解析
    "offset"        DOUBLE PRECISION NOT NULL DEFAULT 0,   -- 秒，正数表示弹幕延后
    scale           DOUBLE PRECISION NOT NULL DEFAULT 1.0, -- 时间缩放系数，纠正线性漂移；只建字段，不出界面
    mode            TEXT             NOT NULL DEFAULT 'snapshot',
    status          TEXT             NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'dead')),
    content_version INT              NOT NULL DEFAULT 0, -- 插入了新弹幕、或清空后重新拉取时加 1
    danmaku_count   INT              NOT NULL DEFAULT 0, -- 在拉取的事务里与弹幕一起维护，读取时不 COUNT
    title           TEXT             NOT NULL, -- 弹幕源的标题，每次拉取都用最新值覆盖
    duration        INT              NOT NULL, -- 弹幕源视频的时长，秒，每次拉取都用最新值覆盖
    last_fetched_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ      NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ      NOT NULL DEFAULT now(),
    -- 同一集不重复绑定同一个弹幕源：ref 是规范化的，同一个弹幕源不论链接怎么写都相同
    UNIQUE (episode_id, adapter, ref)
);

-- snapshot 弹幕：拉取时落库。主键 (binding_id, source_id) 即源内去重：同一个绑定按平台原始弹幕 ID 只存一条。
CREATE TABLE danmaku (
    binding_id BIGINT   NOT NULL REFERENCES bindings (id) ON DELETE CASCADE,
    source_id  BIGINT   NOT NULL, -- 平台原始弹幕 ID
    time_ms    INT      NOT NULL, -- 相对弹幕源视频开头的毫秒数
    mode       SMALLINT NOT NULL, -- 1 滚动、4 底部、5 顶部、6 逆向
    color      INT      NOT NULL CHECK (color BETWEEN 0 AND 16777215), -- RGB888
    text       TEXT     NOT NULL,
    PRIMARY KEY (binding_id, source_id)
);

-- +goose Down
DROP TABLE danmaku;
DROP TABLE bindings;
