-- +goose Up
ALTER TABLE season_bindings ADD COLUMN episode_rule JSONB;

UPDATE season_bindings sb
SET episode_rule = jsonb_build_object(
    'version', 1, 'scope', 'full',
    'rules', (SELECT jsonb_agg(jsonb_build_object('pattern', p.pattern, 'adjustment', 0) ORDER BY p.position)
              FROM unnest(sb.episode_patterns) WITH ORDINALITY AS p(pattern, position)),
    'excludes', '[]'::jsonb);

ALTER TABLE season_bindings
    ALTER COLUMN episode_rule SET NOT NULL,
    ADD CONSTRAINT season_bindings_episode_rule_check CHECK (
        jsonb_typeof(episode_rule) = 'object'
        AND episode_rule ->> 'version' IN ('1', '2')
        AND episode_rule ->> 'scope' IN ('full', 'entry')
        AND jsonb_typeof(episode_rule -> 'rules') = 'array'
        AND jsonb_array_length(episode_rule -> 'rules') >= 1
        AND jsonb_array_length(episode_rule -> 'rules') <= 10
        AND jsonb_typeof(episode_rule -> 'excludes') = 'array'
        AND jsonb_array_length(episode_rule -> 'excludes') <= 10
    ),
    DROP COLUMN episode_patterns;

-- Historical display labels cannot reliably recover the title hierarchy.
ALTER TABLE season_binding_items ADD COLUMN label_parts TEXT[];

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM season_bindings WHERE episode_rule ->> 'version' <> '1') THEN
        RAISE EXCEPTION '存在新版集号规则，拒绝有损回退；请先导出并处理版本 2 配置';
    END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE season_bindings ADD COLUMN episode_patterns TEXT[];
UPDATE season_bindings sb
SET episode_patterns = ARRAY(
    SELECT p.value ->> 'pattern'
    FROM jsonb_array_elements(sb.episode_rule -> 'rules') WITH ORDINALITY AS p(value, position)
    ORDER BY p.position);
ALTER TABLE season_bindings
    ALTER COLUMN episode_patterns SET NOT NULL,
    ADD CONSTRAINT season_bindings_episode_patterns_check CHECK (cardinality(episode_patterns) >= 1),
    DROP COLUMN episode_rule;
ALTER TABLE season_binding_items DROP COLUMN label_parts;
