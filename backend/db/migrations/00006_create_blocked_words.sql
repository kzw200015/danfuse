-- +goose Up
-- 屏蔽词：全局规则，弹弹 API 输出一集的弹幕时去掉正文命中的；保存的弹幕不受影响。只增删，不修改。
CREATE TABLE blocked_words (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind       TEXT        NOT NULL CHECK (kind IN ('keyword', 'regex')),
    pattern    TEXT        NOT NULL, -- 去掉首尾空白的原文，管理界面显示的就是它
    -- 判断重复用的内容（danmaku.BlockedWord.Key）：关键词是归一化后的内容，正则是原文
    dedup_key  TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, dedup_key)
);

-- +goose Down
DROP TABLE blocked_words;
