-- +goose Up
-- 同步记录：每次同步一行，只保留最近 20 次。
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

-- +goose Down
DROP TABLE sync_runs;
