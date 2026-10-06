-- +goose Up
-- 季绑定：一季与一个合集的对应关系，记着集号规则与集号对应。删除季时级联删除。
-- 条目的状态不存储，读取时由条目列表、处理过的记录、绑定和本季的集算出。
CREATE TABLE season_bindings (
    id               BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    season_id        BIGINT      NOT NULL REFERENCES seasons (id) ON DELETE CASCADE,
    adapter          TEXT        NOT NULL, -- 源适配器的 ID
    ref              JSONB       NOT NULL, -- 合集在平台内的引用，由适配器定义，适配器之外不解析
    title            TEXT        NOT NULL, -- 合集标题，每次检查覆盖
    finished         BOOLEAN     NOT NULL DEFAULT false, -- 平台上已完结，每次检查覆盖
    mapping_from     INT         NOT NULL CHECK (mapping_from >= 0), -- 集号对应：合集第 mapping_from 集
    mapping_to       INT         NOT NULL CHECK (mapping_to >= 0),   -- 为本地第 mapping_to 集，之后一一顺延
    episode_pattern  TEXT        NOT NULL, -- 集号规则（见 docs/adr/0005）：空串为内置规则，否则是正则，第一个捕获组为集号
    numbered_by_rule BOOLEAN     NOT NULL, -- 合集的序号由集号规则从条目的标签认出，每次列出时由适配器给出
    follow           BOOLEAN     NOT NULL DEFAULT true, -- 追更
    status           TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'dead')),
    last_error       TEXT,        -- 上次检查结束时的错误；成功列出合集时清掉
    last_checked_at  TIMESTAMPTZ, -- 上次检查的开始时间，由应用写入；被关闭服务打断的检查不写
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 同一季不重复绑定同一个合集：ref 是规范化的，同一个合集不论链接怎么写都相同
    UNIQUE (season_id, adapter, ref)
);

-- 上次检查时的合集条目，按弹幕源 upsert，删掉合集里已经没有的；上次失败的原因按弹幕源保留。
CREATE TABLE season_binding_items (
    season_binding_id BIGINT NOT NULL REFERENCES season_bindings (id) ON DELETE CASCADE,
    ref               JSONB  NOT NULL, -- 弹幕源 ref，与 bindings.ref 同一套格式
    position          INT    NOT NULL, -- 在合集里的顺序，从 1 开始
    number            INT,             -- 合集序号；对不上（含集号重复）时为空
    unmatched_reason  TEXT,            -- 对不上的原因
    label             TEXT   NOT NULL, -- 展示标签，也是集号规则认集号的名称
    last_error        TEXT,            -- 最近一次补建失败的原因，成功后清掉
    last_error_at     TIMESTAMPTZ,
    PRIMARY KEY (season_binding_id, ref)
);

-- 处理过的记录：补建按弹幕源记住处理过的条目，处理过的不再补建，用户删掉的绑定不会被补回来。
-- 集删除时记录随之删除，删掉的集被同步用新 ID 建回来时会重新补建。
CREATE TABLE season_binding_handled (
    season_binding_id BIGINT      NOT NULL REFERENCES season_bindings (id) ON DELETE CASCADE,
    ref               JSONB       NOT NULL,
    episode_id        BIGINT      NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (season_binding_id, ref)
);

CREATE INDEX season_binding_handled_episode_id_idx ON season_binding_handled (episode_id);

-- 建出这个绑定的季绑定：集面板的标签、自动重新拉取的范围、删除季绑定时"一起删"。删除季绑定时置空，绑定变成普通绑定。
ALTER TABLE bindings
    ADD COLUMN season_binding_id BIGINT REFERENCES season_bindings (id) ON DELETE SET NULL;

CREATE INDEX bindings_season_binding_id_idx ON bindings (season_binding_id);

-- +goose Down
DROP INDEX bindings_season_binding_id_idx;
ALTER TABLE bindings DROP COLUMN season_binding_id;
DROP TABLE season_binding_handled;
DROP TABLE season_binding_items;
DROP TABLE season_bindings;
