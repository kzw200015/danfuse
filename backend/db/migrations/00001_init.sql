-- +goose Up
-- 目录：剧 → 季 → 集，同步按自然键 upsert。删除剧、季时级联删除下级。

-- 图片：只存剧的主海报，季和集沿用剧的海报。每张图只被一部剧引用（同一张图不在剧之间共享），
-- 剧换下来的旧图、删除剧时它的海报都一并删除，表里没有不被任何剧引用的图片。
CREATE TABLE images (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    content_type TEXT        NOT NULL,
    data         BYTEA       NOT NULL,
    sha256       BYTEA       NOT NULL, -- data 的 SHA-256，同步时与目录源的新图比较，内容没变就不重写
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE series (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    type            TEXT        NOT NULL CHECK (type IN ('tv', 'movie')),
    title           TEXT        NOT NULL,
    original_title  TEXT,
    year            INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- 海报不级联：删除剧时由业务代码在同一事务里删掉它的海报；同步换图为插入新图 → 剧指向新图 → 删除旧图。
    -- UNIQUE 约束住"每张图只被一部剧引用"，它的索引也让删除图片时的外键检查不用扫全表。
    poster_image_id BIGINT UNIQUE REFERENCES images (id),
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

-- 同步记录：每次同步一行，只保留最近若干次（sync.keep_runs）。
CREATE TABLE sync_runs (
    id               BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    trigger          TEXT        NOT NULL CHECK (trigger IN ('manual', 'schedule')),
    status           TEXT        NOT NULL CHECK (status IN ('running', 'succeeded', 'failed', 'interrupted')),
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at      TIMESTAMPTZ,
    total            INT, -- 剧（含电影）的总数，列完媒体库之前为空
    done             INT         NOT NULL DEFAULT 0,
    created_series   INT         NOT NULL DEFAULT 0,
    created_seasons  INT         NOT NULL DEFAULT 0,
    created_episodes INT         NOT NULL DEFAULT 0,
    warnings         JSONB       NOT NULL DEFAULT '[]', -- 只保存前 200 条
    warning_count    INT         NOT NULL DEFAULT 0,    -- 警告总数
    error            TEXT                               -- 失败原因
);

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
    follow           BOOLEAN     NOT NULL DEFAULT true, -- 追更
    status           TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'dead')),
    last_error       TEXT,        -- 上次检查结束时的错误；成功列出合集时清掉
    last_checked_at  TIMESTAMPTZ, -- 上次检查的开始时间，由应用写入；被关闭服务打断的检查不写
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    episode_patterns TEXT[]      NOT NULL CHECK (cardinality(episode_patterns) >= 1), -- 集号规则（见 docs/adr/0005）：按优先级排列的正则，第一条匹配上的给出集号
    numbered_by_rule BOOLEAN     NOT NULL, -- 合集的序号由集号规则从条目的标签认出，每次列出时由适配器给出
    -- 同一季不重复绑定同一个合集：ref 是规范化的，同一个合集不论链接怎么写都相同
    UNIQUE (season_id, adapter, ref)
);

-- 绑定：一集与一个弹幕源的对应关系。删除集时级联删除它的绑定，删除绑定时级联删除它的弹幕。
-- kind 区分弹幕源的形态（见 docs/adr/0004）：link 是贴链接或补建出的，按适配器和 ref 能重新拉取；
-- file 是上传的一组弹幕文件，没有适配器和 ref（唯一约束 (episode_id, adapter, ref) 在 NULL 上不生效，同一集可以有多个），
-- 也没有视频时长，不会失效，不由季绑定建出。
CREATE TABLE bindings (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    episode_id        BIGINT           NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    adapter           TEXT, -- 源适配器的 ID，一经发布不能再改；文件绑定为空
    ref               JSONB, -- 弹幕源在平台内的引用，由适配器定义，适配器之外不解析；文件绑定为空
    "offset"          DOUBLE PRECISION NOT NULL DEFAULT 0,   -- 秒，正数表示弹幕延后
    scale             DOUBLE PRECISION NOT NULL DEFAULT 1.0, -- 时间缩放系数，纠正线性漂移；只建字段，不出界面
    status            TEXT             NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'dead')),
    content_version   INT              NOT NULL DEFAULT 0, -- 插入了新弹幕、或清空后重新拉取时加 1
    danmaku_count     INT              NOT NULL DEFAULT 0, -- 在拉取的事务里与弹幕一起维护，读取时不 COUNT
    title             TEXT             NOT NULL, -- 弹幕源的标题，每次拉取都用最新值覆盖
    duration          INT, -- 弹幕源视频的时长，秒，每次拉取都用最新值覆盖；文件绑定为空
    last_fetched_at   TIMESTAMPTZ,
    created_at        TIMESTAMPTZ      NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ      NOT NULL DEFAULT now(),
    -- 建出这个绑定的季绑定：集面板的标签、自动重新拉取的范围、删除季绑定时"一起删"。删除季绑定时置空，绑定变成普通绑定。
    season_binding_id BIGINT REFERENCES season_bindings (id) ON DELETE SET NULL,
    kind              TEXT             NOT NULL DEFAULT 'link' CHECK (kind IN ('link', 'file')),
    file_count        INT              NOT NULL DEFAULT 0, -- 弹幕文件的份数，在追加文件的事务里维护，读取时不 COUNT
    -- 同一集不重复绑定同一个弹幕源：ref 是规范化的，同一个弹幕源不论链接怎么写都相同
    UNIQUE (episode_id, adapter, ref),
    CONSTRAINT bindings_kind_columns_check CHECK (
        (kind = 'link' AND adapter IS NOT NULL AND ref IS NOT NULL AND duration IS NOT NULL AND file_count = 0)
        OR (kind = 'file' AND adapter IS NULL AND ref IS NULL AND duration IS NULL AND status = 'active'
            AND last_fetched_at IS NULL AND season_binding_id IS NULL)
    )
);

CREATE INDEX bindings_season_binding_id_idx ON bindings (season_binding_id);

-- 弹幕：拉取时落库。主键 (binding_id, source_id) 即源内去重：同一个绑定按平台原始弹幕 ID 只存一条。
CREATE TABLE danmaku (
    binding_id BIGINT   NOT NULL REFERENCES bindings (id) ON DELETE CASCADE,
    source_id  BIGINT   NOT NULL, -- 平台原始弹幕 ID
    time_ms    INT      NOT NULL, -- 相对弹幕源视频开头的毫秒数
    mode       SMALLINT NOT NULL, -- 1 滚动、4 底部、5 顶部、6 逆向
    color      INT      NOT NULL CHECK (color BETWEEN 0 AND 16777215), -- RGB888
    text       TEXT     NOT NULL,
    PRIMARY KEY (binding_id, source_id)
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

-- 租约：应用的锁（见 database.TryLease、docs/adr/0003）。持有者定时续约，过期后别人才能接管；释放时删除这一行。
-- 进程崩溃时留下的行等过期后被下一个持有者覆盖；之后再也不会被拿的键（例如已删除的季绑定）留下的行不清理。
CREATE SEQUENCE lease_tokens;

CREATE TABLE leases (
    key        TEXT PRIMARY KEY,
    token      BIGINT      NOT NULL, -- fencing token：每次拿到锁从 lease_tokens 取一个新值，续约、释放都核对它
    expires_at TIMESTAMPTZ NOT NULL  -- 按数据库的 now() 判断过期
);

-- +goose Down
DROP TABLE leases;
DROP SEQUENCE lease_tokens;
DROP TABLE season_binding_handled;
DROP TABLE season_binding_items;
DROP TABLE binding_files;
DROP TABLE danmaku;
DROP TABLE bindings;
DROP TABLE season_bindings;
DROP TABLE sync_runs;
DROP TABLE episodes;
DROP TABLE seasons;
DROP TABLE series;
DROP TABLE images;
