-- 由 make schema 根据 db/migrations 生成，不要手改。表结构以迁移为准，这里只用来查看。

CREATE SEQUENCE lease_tokens;

CREATE TABLE blocked_words (
    id         bigint                   GENERATED ALWAYS AS IDENTITY,
    kind       text                     NOT NULL,
    pattern    text                     NOT NULL,
    dedup_key  text                     NOT NULL,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT blocked_words_pkey PRIMARY KEY (id),
    CONSTRAINT blocked_words_kind_dedup_key_key UNIQUE (kind, dedup_key),
    CONSTRAINT blocked_words_kind_check CHECK (kind = ANY (ARRAY['keyword'::text, 'regex'::text]))
);

CREATE TABLE images (
    id           bigint                   GENERATED ALWAYS AS IDENTITY,
    content_type text                     NOT NULL,
    data         bytea                    NOT NULL,
    sha256       bytea                    NOT NULL,
    created_at   timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT images_pkey PRIMARY KEY (id)
);

CREATE TABLE leases (
    key        text                     NOT NULL,
    token      bigint                   NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    CONSTRAINT leases_pkey PRIMARY KEY (key)
);

CREATE TABLE sync_runs (
    id               bigint                   GENERATED ALWAYS AS IDENTITY,
    trigger          text                     NOT NULL,
    status           text                     NOT NULL,
    started_at       timestamp with time zone NOT NULL DEFAULT now(),
    finished_at      timestamp with time zone,
    total            integer,
    done             integer                  NOT NULL DEFAULT 0,
    created_series   integer                  NOT NULL DEFAULT 0,
    created_seasons  integer                  NOT NULL DEFAULT 0,
    created_episodes integer                  NOT NULL DEFAULT 0,
    warnings         jsonb                    NOT NULL DEFAULT '[]'::jsonb,
    warning_count    integer                  NOT NULL DEFAULT 0,
    error            text,
    CONSTRAINT sync_runs_pkey PRIMARY KEY (id),
    CONSTRAINT sync_runs_status_check CHECK (status = ANY (ARRAY['running'::text, 'succeeded'::text, 'failed'::text, 'interrupted'::text])),
    CONSTRAINT sync_runs_trigger_check CHECK (trigger = ANY (ARRAY['manual'::text, 'schedule'::text]))
);

CREATE TABLE series (
    id                bigint                   GENERATED ALWAYS AS IDENTITY,
    type              text                     NOT NULL,
    title             text                     NOT NULL,
    original_title    text,
    year              integer,
    created_at        timestamp with time zone NOT NULL DEFAULT now(),
    updated_at        timestamp with time zone NOT NULL DEFAULT now(),
    poster_image_id   bigint,
    tmdb_id           bigint,
    catalog_source_id text,
    premiere_date     date,
    CONSTRAINT series_pkey PRIMARY KEY (id),
    CONSTRAINT series_poster_image_id_key UNIQUE (poster_image_id),
    CONSTRAINT series_type_tmdb_id_key UNIQUE (type, tmdb_id),
    CONSTRAINT series_tmdb_id_check CHECK (tmdb_id > 0),
    CONSTRAINT series_type_check CHECK (type = ANY (ARRAY['tv'::text, 'movie'::text])),
    CONSTRAINT series_poster_image_id_fkey FOREIGN KEY (poster_image_id) REFERENCES images(id)
);
CREATE UNIQUE INDEX series_natural_key_idx ON series (type, title, year) NULLS NOT DISTINCT WHERE tmdb_id IS NULL;

CREATE TABLE seasons (
    id            bigint                   GENERATED ALWAYS AS IDENTITY,
    series_id     bigint                   NOT NULL,
    number        integer                  NOT NULL,
    title         text,
    created_at    timestamp with time zone NOT NULL DEFAULT now(),
    updated_at    timestamp with time zone NOT NULL DEFAULT now(),
    search_vector tsvector                 NOT NULL DEFAULT ''::tsvector,
    CONSTRAINT seasons_pkey PRIMARY KEY (id),
    CONSTRAINT seasons_series_id_number_key UNIQUE (series_id, number),
    CONSTRAINT seasons_number_check CHECK (number >= 0),
    CONSTRAINT seasons_series_id_fkey FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE
);
CREATE INDEX seasons_search_vector_idx ON seasons USING gin (search_vector);

CREATE TABLE episodes (
    id         bigint                   GENERATED ALWAYS AS IDENTITY,
    season_id  bigint                   NOT NULL,
    number     integer                  NOT NULL,
    title      text,
    duration   integer,
    created_at timestamp with time zone NOT NULL DEFAULT now(),
    updated_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT episodes_pkey PRIMARY KEY (id),
    CONSTRAINT episodes_season_id_number_key UNIQUE (season_id, number),
    CONSTRAINT episodes_number_check CHECK (number >= 0),
    CONSTRAINT episodes_season_id_fkey FOREIGN KEY (season_id) REFERENCES seasons(id) ON DELETE CASCADE
);

CREATE TABLE season_bindings (
    id               bigint                   GENERATED ALWAYS AS IDENTITY,
    season_id        bigint                   NOT NULL,
    adapter          text,
    ref              jsonb,
    title            text                     NOT NULL,
    mapping_from     integer                  NOT NULL,
    mapping_to       integer                  NOT NULL,
    follow           boolean                  NOT NULL DEFAULT true,
    status           text                     NOT NULL DEFAULT 'active'::text,
    last_error       text,
    last_checked_at  timestamp with time zone,
    created_at       timestamp with time zone NOT NULL DEFAULT now(),
    episode_patterns text[]                   NOT NULL,
    numbered_by_rule boolean,
    kind             text                     NOT NULL DEFAULT 'collection'::text,
    link             text,
    CONSTRAINT season_bindings_pkey PRIMARY KEY (id),
    CONSTRAINT season_bindings_episode_patterns_check CHECK (cardinality(episode_patterns) >= 1),
    CONSTRAINT season_bindings_kind_check CHECK (kind = ANY (ARRAY['collection'::text, 'folder'::text])),
    CONSTRAINT season_bindings_kind_columns_check CHECK (kind = 'collection'::text AND adapter IS NOT NULL AND ref IS NOT NULL AND link IS NOT NULL AND numbered_by_rule IS NOT NULL OR kind = 'folder'::text AND adapter IS NULL AND ref IS NULL AND link IS NULL AND numbered_by_rule IS NULL AND NOT follow AND status = 'active'::text AND last_checked_at IS NULL AND last_error IS NULL),
    CONSTRAINT season_bindings_mapping_from_check CHECK (mapping_from >= 0),
    CONSTRAINT season_bindings_mapping_to_check CHECK (mapping_to >= 0),
    CONSTRAINT season_bindings_status_check CHECK (status = ANY (ARRAY['active'::text, 'dead'::text])),
    CONSTRAINT season_bindings_season_id_fkey FOREIGN KEY (season_id) REFERENCES seasons(id) ON DELETE CASCADE
);
CREATE INDEX season_bindings_season_id_idx ON season_bindings (season_id);

CREATE TABLE season_binding_entries (
    id                bigint  GENERATED ALWAYS AS IDENTITY,
    season_binding_id bigint  NOT NULL,
    label             text    NOT NULL,
    "position"        integer NOT NULL,
    CONSTRAINT season_binding_entries_pkey PRIMARY KEY (id),
    CONSTRAINT season_binding_entries_id_season_binding_id_key UNIQUE (id, season_binding_id),
    CONSTRAINT season_binding_entries_season_binding_id_label_key UNIQUE (season_binding_id, label),
    CONSTRAINT season_binding_entries_season_binding_id_fkey FOREIGN KEY (season_binding_id) REFERENCES season_bindings(id) ON DELETE CASCADE
);

CREATE TABLE season_binding_items (
    season_binding_id bigint                   NOT NULL,
    ref               jsonb                    NOT NULL,
    "position"        integer                  NOT NULL,
    number            integer,
    label             text                     NOT NULL,
    last_error        text,
    last_error_at     timestamp with time zone,
    CONSTRAINT season_binding_items_pkey PRIMARY KEY (season_binding_id, ref),
    CONSTRAINT season_binding_items_season_binding_id_fkey FOREIGN KEY (season_binding_id) REFERENCES season_bindings(id) ON DELETE CASCADE
);

CREATE TABLE bindings (
    id                 bigint                   GENERATED ALWAYS AS IDENTITY,
    episode_id         bigint                   NOT NULL,
    adapter            text,
    ref                jsonb,
    "offset"           double precision         NOT NULL DEFAULT 0,
    status             text                     NOT NULL DEFAULT 'active'::text,
    content_version    integer                  NOT NULL DEFAULT 0,
    danmaku_count      integer                  NOT NULL DEFAULT 0,
    title              text                     NOT NULL,
    duration           integer,
    last_fetched_at    timestamp with time zone,
    created_at         timestamp with time zone NOT NULL DEFAULT now(),
    season_binding_id  bigint,
    kind               text                     NOT NULL DEFAULT 'link'::text,
    file_count         integer                  NOT NULL DEFAULT 0,
    fetch_attempted_at timestamp with time zone,
    max_time_ms        integer                  NOT NULL DEFAULT 0,
    entry_id           bigint,
    CONSTRAINT bindings_pkey PRIMARY KEY (id),
    CONSTRAINT bindings_episode_id_adapter_ref_key UNIQUE (episode_id, adapter, ref),
    CONSTRAINT bindings_season_binding_id_episode_id_key UNIQUE (season_binding_id, episode_id),
    CONSTRAINT bindings_entry_id_check CHECK (kind = 'link'::text AND entry_id IS NULL OR kind = 'file'::text AND (season_binding_id IS NULL) = (entry_id IS NULL)),
    CONSTRAINT bindings_file_fetch_attempted_check CHECK (kind = 'link'::text OR fetch_attempted_at IS NULL),
    CONSTRAINT bindings_kind_check CHECK (kind = ANY (ARRAY['link'::text, 'file'::text])),
    CONSTRAINT bindings_kind_columns_check CHECK (kind = 'link'::text AND adapter IS NOT NULL AND ref IS NOT NULL AND duration IS NOT NULL AND file_count = 0 OR kind = 'file'::text AND adapter IS NULL AND ref IS NULL AND duration IS NULL AND status = 'active'::text AND last_fetched_at IS NULL),
    CONSTRAINT bindings_status_check CHECK (status = ANY (ARRAY['active'::text, 'dead'::text])),
    CONSTRAINT bindings_entry_id_fkey FOREIGN KEY (entry_id, season_binding_id) REFERENCES season_binding_entries(id, season_binding_id) ON DELETE CASCADE,
    CONSTRAINT bindings_episode_id_fkey FOREIGN KEY (episode_id) REFERENCES episodes(id) ON DELETE CASCADE,
    CONSTRAINT bindings_season_binding_id_fkey FOREIGN KEY (season_binding_id) REFERENCES season_bindings(id) ON DELETE CASCADE
);
CREATE INDEX bindings_link_created_at_idx ON bindings (created_at) WHERE kind = 'link'::text;

CREATE TABLE season_binding_entry_files (
    id       bigint  GENERATED ALWAYS AS IDENTITY,
    entry_id bigint  NOT NULL,
    name     text    NOT NULL,
    sha256   bytea   NOT NULL,
    size     integer NOT NULL,
    content  bytea   NOT NULL,
    CONSTRAINT season_binding_entry_files_pkey PRIMARY KEY (id),
    CONSTRAINT season_binding_entry_files_entry_id_sha256_key UNIQUE (entry_id, sha256),
    CONSTRAINT season_binding_entry_files_entry_id_fkey FOREIGN KEY (entry_id) REFERENCES season_binding_entries(id) ON DELETE CASCADE
);

CREATE TABLE binding_files (
    id          bigint                   GENERATED ALWAYS AS IDENTITY,
    binding_id  bigint                   NOT NULL,
    name        text                     NOT NULL,
    sha256      bytea                    NOT NULL,
    size        integer                  NOT NULL,
    content     bytea                    NOT NULL,
    uploaded_at timestamp with time zone NOT NULL DEFAULT now(),
    CONSTRAINT binding_files_pkey PRIMARY KEY (id),
    CONSTRAINT binding_files_binding_id_sha256_key UNIQUE (binding_id, sha256),
    CONSTRAINT binding_files_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES bindings(id) ON DELETE CASCADE
);

CREATE TABLE danmaku (
    binding_id bigint   NOT NULL,
    source_id  bigint   NOT NULL,
    time_ms    integer  NOT NULL,
    mode       smallint NOT NULL,
    color      integer  NOT NULL,
    text       text     NOT NULL,
    CONSTRAINT danmaku_pkey PRIMARY KEY (binding_id, source_id),
    CONSTRAINT danmaku_color_check CHECK (color >= 0 AND color <= 16777215),
    CONSTRAINT danmaku_binding_id_fkey FOREIGN KEY (binding_id) REFERENCES bindings(id) ON DELETE CASCADE
);
