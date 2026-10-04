-- +goose Up
-- 图片：本里程碑只存剧的主海报，季和集沿用剧的海报。每张图只被一部剧引用（同一张图不在剧之间共享），
-- 剧换下来的旧图、删除剧时它的海报都一并删除，表里没有不被任何剧引用的图片。
CREATE TABLE images (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    content_type TEXT        NOT NULL,
    data         BYTEA       NOT NULL,
    sha256       BYTEA       NOT NULL, -- data 的 SHA-256，同步时与目录源的新图比较，内容没变就不重写
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 不级联：删除剧时由业务代码在同一事务里删掉它的海报；同步换图为插入新图 → 剧指向新图 → 删除旧图。
-- UNIQUE 约束住"每张图只被一部剧引用"，它的索引也让删除图片时的外键检查不用扫全表。
ALTER TABLE series ADD COLUMN poster_image_id BIGINT UNIQUE REFERENCES images (id);

-- +goose Down
ALTER TABLE series DROP COLUMN poster_image_id;
DROP TABLE images;
