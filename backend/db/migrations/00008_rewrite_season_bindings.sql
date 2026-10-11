-- +goose Up
-- 季绑定保存一份表单，各集的绑定一律按表单建出（见 docs/adr/0010）：
--   - 合集的季绑定：表单是贴的链接、选的合集、集号规则、集号对应；season_binding_items 只是上次列出的结果，用来展示和记录失败原因。
--   - 文件夹的季绑定：表单是各个条目（season_binding_entries）及其原文件（season_binding_entry_files）、集号规则、集号对应；
--     建出的文件绑定用 entry_id 指向条目，不持有文件（没有 binding_files 行，file_count 为 0），从条目的文件解析。
--   - 槽位：一个季绑定在一集上至多一个它建出的绑定，即唯一约束 (season_binding_id, episode_id)。删除季绑定一律连同它建出的绑定。
--   - 不再记住处理过的条目：删掉 season_binding_handled。
-- 迁移只转数据，不按新规则修正现有的绑定：与表单不一致的，由下一次补建（只新建、替换）或保存表单（还会删掉没有条目的槽位）处理。

-- 1. 加列、加表。先去掉按种类守列的旧约束，最后按新的规则加回。
ALTER TABLE season_bindings
    DROP CONSTRAINT season_bindings_kind_columns_check,
    -- 合集的季绑定创建（或换合集）时贴的链接，编辑时预填。只用来回显：合集以 ref 为准，同一个合集可以由不同的链接贴进来
    ADD COLUMN link TEXT;

-- 文件夹的条目：子目录名，或去掉扩展名的文件名（不带文件夹名）。
-- (id, season_binding_id) 供 bindings 的复合外键引用，保证建出的文件绑定指向同一个季绑定的条目。
CREATE TABLE season_binding_entries (
    id                BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    season_binding_id BIGINT NOT NULL REFERENCES season_bindings (id) ON DELETE CASCADE,
    label             TEXT   NOT NULL, -- 条目名称，集号规则从它认出序号
    position          INT    NOT NULL, -- 在文件夹里的顺序，从 1 开始
    UNIQUE (season_binding_id, label),
    UNIQUE (id, season_binding_id)
);

-- 条目的原文件：建出的文件绑定的弹幕从这里解析。同一个条目里按内容去重。
CREATE TABLE season_binding_entry_files (
    id       BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    entry_id BIGINT NOT NULL REFERENCES season_binding_entries (id) ON DELETE CASCADE,
    name     TEXT   NOT NULL, -- 上传时的文件名
    sha256   BYTEA  NOT NULL, -- 换文件夹时按"名称 + 文件哈希的集合"判断条目没变
    size     INT    NOT NULL, -- 字节
    content  BYTEA  NOT NULL,
    UNIQUE (entry_id, sha256)
);

ALTER TABLE bindings
    ADD COLUMN entry_id BIGINT; -- 文件夹的季绑定建出的文件绑定所对的条目

-- 2. 老的合集季绑定按 ref 拼出链接（一次性的快照，与适配器之后的变化无关）。多 P 投稿用 av 号，链接识别也认它。
UPDATE season_bindings
SET link = CASE ref ->> 'kind'
               WHEN 'bangumi' THEN 'https://www.bilibili.com/bangumi/play/ss' || (ref ->> 'seasonId')
               WHEN 'ugcSeason' THEN 'https://space.bilibili.com/' || (ref ->> 'mid') || '/lists/' || (ref ->> 'seasonId')
                   || '?type=season'
               WHEN 'multiPage' THEN 'https://www.bilibili.com/video/av' || (ref ->> 'aid')
           END
WHERE kind = 'collection';

-- 3. 条目表的 number 改为平台给的集号：按规则编号的合集清空（序号读取时按集号规则现算），番剧的保留。
UPDATE season_binding_items i
SET number = NULL
FROM season_bindings sb
WHERE sb.id = i.season_binding_id
  AND sb.numbered_by_rule;

-- 4. 槽位冲突（改过集号对应、换过合集留下的）：同一个季绑定在同一集上有多个绑定时留 id 最大的，
-- 其余的置空来源，变回普通绑定，弹幕不丢。文件夹的季绑定上传时各条目的目标集互不相同，不会冲突，一并处理只为稳妥。
UPDATE bindings b
SET season_binding_id = NULL
WHERE EXISTS (SELECT 1
              FROM bindings o
              WHERE o.season_binding_id = b.season_binding_id
                AND o.episode_id = b.episode_id
                AND o.id > b.id);

-- 5. 老的文件夹季绑定：每个建出的绑定转成一个条目，名称取绑定标题去掉开头的"季绑定标题 / "，位置按绑定 id；
-- 绑定的标题也改为条目名称。
INSERT INTO season_binding_entries (season_binding_id, label, position)
SELECT b.season_binding_id,
       CASE WHEN starts_with(b.title, sb.title || ' / ') THEN substr(b.title, length(sb.title) + 4) ELSE b.title END,
       row_number() OVER (PARTITION BY b.season_binding_id ORDER BY b.id)
FROM bindings b
JOIN season_bindings sb ON sb.id = b.season_binding_id
WHERE sb.kind = 'folder';

UPDATE bindings b
SET entry_id = e.id,
    title    = e.label
FROM season_bindings sb, season_binding_entries e
WHERE sb.id = b.season_binding_id
  AND sb.kind = 'folder'
  AND e.season_binding_id = sb.id
  AND e.label = CASE WHEN starts_with(b.title, sb.title || ' / ') THEN substr(b.title, length(sb.title) + 4)
                     ELSE b.title END;

-- 原文件从绑定移进条目，绑定的份数归零
INSERT INTO season_binding_entry_files (entry_id, name, sha256, size, content)
SELECT b.entry_id, f.name, f.sha256, f.size, f.content
FROM binding_files f
JOIN bindings b ON b.id = f.binding_id
WHERE b.entry_id IS NOT NULL
ORDER BY f.id;

DELETE FROM binding_files f
USING bindings b
WHERE b.id = f.binding_id
  AND b.entry_id IS NOT NULL;

UPDATE bindings
SET file_count = 0
WHERE entry_id IS NOT NULL;

-- 当初的目标集是预览后逐个选定的，没有保存集号规则和集号对应：填默认规则的快照（source.DefaultEpisodeRule）和 1 = 1，
-- 不按现有绑定反推。派生结果对不上现在各绑定所在的集时，编辑的预览会写明，保存或立即补建时替换。
UPDATE season_bindings
SET episode_patterns = ARRAY [
        '(?i)\bS(?P<season>\d{1,4}) ?E(?P<episode>\d{1,4})\b',
        '第 ?(?P<episode>\d{1,4}) ?[话話集]',
        '(?i)\bEP ?(?P<episode>\d{1,4})\b',
        '/ (?P<episode>\d{1,4})$'
        ],
    mapping_from     = 1,
    mapping_to       = 1
WHERE kind = 'folder';

-- 建出的绑定已被删光（没有条目）的文件夹季绑定删掉
DELETE FROM season_bindings sb
WHERE kind = 'folder'
  AND NOT EXISTS (SELECT 1 FROM season_binding_entries e WHERE e.season_binding_id = sb.id);

-- 6. 加约束，改外键，去掉不再用的表和列。
-- 两种季绑定都有集号规则和集号对应；只属于合集的列按 kind 守住，文件夹的季绑定这些列为空，不追更（follow 默认开着，插入时要写明 false），不会失效。
-- 唯一约束 (season_id, adapter, ref) 不变，只作用于合集的季绑定。
ALTER TABLE season_bindings
    ALTER COLUMN episode_patterns SET NOT NULL,
    ALTER COLUMN mapping_from SET NOT NULL,
    ALTER COLUMN mapping_to SET NOT NULL,
    ADD CONSTRAINT season_bindings_kind_columns_check CHECK (
        (kind = 'collection' AND adapter IS NOT NULL AND ref IS NOT NULL AND link IS NOT NULL
            AND numbered_by_rule IS NOT NULL)
        OR (kind = 'folder' AND adapter IS NULL AND ref IS NULL AND link IS NULL AND numbered_by_rule IS NULL
            AND NOT follow AND status = 'active' AND last_checked_at IS NULL AND last_error IS NULL)
    ),
    DROP COLUMN finished,
    DROP COLUMN updated_at;

-- 对不上的原因读取时由派生现算
ALTER TABLE season_binding_items
    DROP COLUMN unmatched_reason;

DROP TABLE season_binding_handled;

-- 建出的绑定：
--   - 删除季绑定时级联删除；
--   - 每个槽位（季绑定，集）至多一个，槽位约束的索引也供按季绑定查绑定、外键的级联，原来的单列索引不再需要；
--   - 文件夹的季绑定建出的文件绑定指向同一个季绑定的条目，条目删除时级联删除。链接绑定的 entry_id 为空，
--     MATCH SIMPLE 下不检查；文件绑定的 season_binding_id 与 entry_id 要么都有（建出的），要么都没有（在集上上传的）。
-- (episode_id, adapter, ref) 照旧：同一集同一个弹幕源只一个绑定，补建撞上它时按"已有同源绑定"跳过。
ALTER TABLE bindings
    DROP CONSTRAINT bindings_season_binding_id_fkey,
    ADD CONSTRAINT bindings_season_binding_id_fkey
        FOREIGN KEY (season_binding_id) REFERENCES season_bindings (id) ON DELETE CASCADE,
    ADD CONSTRAINT bindings_season_binding_id_episode_id_key UNIQUE (season_binding_id, episode_id),
    ADD CONSTRAINT bindings_entry_id_fkey
        FOREIGN KEY (entry_id, season_binding_id) REFERENCES season_binding_entries (id, season_binding_id)
            ON DELETE CASCADE,
    ADD CONSTRAINT bindings_entry_id_check CHECK (
        (kind = 'link' AND entry_id IS NULL)
        OR (kind = 'file' AND (season_binding_id IS NULL) = (entry_id IS NULL))
    ),
    DROP COLUMN scale,
    DROP COLUMN updated_at;

DROP INDEX bindings_season_binding_id_idx;

-- +goose Down
-- 结构回退到 00007，数据尽量还原；不保证新模型的数据能无损地回到旧模型（例如没有建出绑定的条目连同文件丢掉）。
CREATE INDEX bindings_season_binding_id_idx ON bindings (season_binding_id);

ALTER TABLE bindings
    DROP CONSTRAINT bindings_entry_id_check,
    DROP CONSTRAINT bindings_entry_id_fkey,
    DROP CONSTRAINT bindings_season_binding_id_episode_id_key,
    DROP CONSTRAINT bindings_season_binding_id_fkey,
    ADD CONSTRAINT bindings_season_binding_id_fkey
        FOREIGN KEY (season_binding_id) REFERENCES season_bindings (id) ON DELETE SET NULL,
    ADD COLUMN scale DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- 条目的文件移回建出的绑定，重算份数，标题补回"文件夹名 / "
INSERT INTO binding_files (binding_id, name, sha256, size, content)
SELECT b.id, f.name, f.sha256, f.size, f.content
FROM bindings b
JOIN season_binding_entry_files f ON f.entry_id = b.entry_id
ORDER BY f.id;

UPDATE bindings b
SET file_count = (SELECT count(*) FROM binding_files f WHERE f.binding_id = b.id),
    title      = sb.title || ' / ' || b.title
FROM season_bindings sb
WHERE sb.id = b.season_binding_id
  AND b.entry_id IS NOT NULL;

ALTER TABLE bindings
    DROP COLUMN entry_id;

DROP TABLE season_binding_entry_files;
DROP TABLE season_binding_entries;

-- 处理过的记录由建出的链接绑定重建
CREATE TABLE season_binding_handled (
    season_binding_id BIGINT      NOT NULL REFERENCES season_bindings (id) ON DELETE CASCADE,
    ref               JSONB       NOT NULL,
    episode_id        BIGINT      NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (season_binding_id, ref)
);

CREATE INDEX season_binding_handled_episode_id_idx ON season_binding_handled (episode_id);

INSERT INTO season_binding_handled (season_binding_id, ref, episode_id, created_at)
SELECT season_binding_id, ref, episode_id, created_at
FROM bindings
WHERE kind = 'link'
  AND season_binding_id IS NOT NULL
ON CONFLICT DO NOTHING;

ALTER TABLE season_binding_items
    ADD COLUMN unmatched_reason TEXT;

-- 文件夹的季绑定丢掉集号规则和集号对应
ALTER TABLE season_bindings
    DROP CONSTRAINT season_bindings_kind_columns_check,
    ALTER COLUMN episode_patterns DROP NOT NULL,
    ALTER COLUMN mapping_from DROP NOT NULL,
    ALTER COLUMN mapping_to DROP NOT NULL,
    DROP COLUMN link,
    ADD COLUMN finished BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

UPDATE season_bindings
SET episode_patterns = NULL,
    mapping_from     = NULL,
    mapping_to       = NULL
WHERE kind = 'folder';

ALTER TABLE season_bindings
    ADD CONSTRAINT season_bindings_kind_columns_check CHECK (
        (kind = 'collection' AND adapter IS NOT NULL AND ref IS NOT NULL AND mapping_from IS NOT NULL
            AND mapping_to IS NOT NULL AND episode_patterns IS NOT NULL AND numbered_by_rule IS NOT NULL)
        OR (kind = 'folder' AND adapter IS NULL AND ref IS NULL AND mapping_from IS NULL AND mapping_to IS NULL
            AND episode_patterns IS NULL AND numbered_by_rule IS NULL AND NOT follow AND NOT finished
            AND status = 'active' AND last_checked_at IS NULL AND last_error IS NULL)
    );
