-- +goose Up
-- 处理过的记录从此只记季绑定自己建出过的条目（见 docs/adr/0001）。以前补建时对应的集上已有同一个弹幕源的绑定也记一条，
-- 删掉这些记录：条目回到"集上已有"，那个绑定被删掉后照常补建。
-- 补建出的绑定被删掉、又手动绑回来的条目分不出来，一并删掉。
DELETE FROM season_binding_handled h
USING season_bindings sb
WHERE sb.id = h.season_binding_id
  AND EXISTS (SELECT 1
              FROM bindings b
              WHERE b.episode_id = h.episode_id
                AND b.adapter = sb.adapter
                AND b.ref = h.ref
                AND b.season_binding_id IS DISTINCT FROM sb.id);

-- +goose Down
-- 删掉的记录恢复不了，什么都不做。
