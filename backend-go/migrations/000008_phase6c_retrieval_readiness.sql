CREATE TABLE IF NOT EXISTS projects (
    id BIGSERIAL PRIMARY KEY,
    slug VARCHAR(80) NOT NULL UNIQUE,
    name VARCHAR(160) NOT NULL,
    visibility VARCHAR(24) NOT NULL DEFAULT 'public'
        CHECK (visibility IN ('public', 'private')),
    status VARCHAR(24) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'archived')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO projects (id, slug, name, visibility, status)
VALUES (1, 'public-community', 'Public Community', 'public', 'active')
ON CONFLICT (id) DO NOTHING;

SELECT setval('projects_id_seq', GREATEST((SELECT COALESCE(MAX(id), 1) FROM projects), 1), true);

CREATE TABLE IF NOT EXISTS project_members (
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role VARCHAR(24) NOT NULL DEFAULT 'member'
        CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    status VARCHAR(24) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);

INSERT INTO project_members (project_id, user_id, role, status)
SELECT 1, id, CASE WHEN role = 'admin' THEN 'admin' ELSE 'member' END, 'active'
FROM users
ON CONFLICT (project_id, user_id) DO NOTHING;

CREATE OR REPLACE FUNCTION ensure_default_project_membership()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO project_members (project_id, user_id, role, status)
    VALUES (1, NEW.id, CASE WHEN NEW.role = 'admin' THEN 'admin' ELSE 'member' END, 'active')
    ON CONFLICT (project_id, user_id) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_users_default_project ON users;
CREATE TRIGGER trg_users_default_project
AFTER INSERT ON users
FOR EACH ROW EXECUTE FUNCTION ensure_default_project_membership();

CREATE TABLE IF NOT EXISTS datasets (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    slug VARCHAR(80) NOT NULL,
    name VARCHAR(160) NOT NULL,
    description TEXT,
    status VARCHAR(24) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'frozen', 'archived')),
    version BIGINT NOT NULL DEFAULT 1,
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, slug)
);

INSERT INTO datasets (id, project_id, slug, name, description, status, version)
VALUES (1, 1, 'community-current', 'Community Current', 'Default retrieval-ready community dataset', 'active', 1)
ON CONFLICT (id) DO NOTHING;

SELECT setval('datasets_id_seq', GREATEST((SELECT COALESCE(MAX(id), 1) FROM datasets), 1), true);

ALTER TABLE notes ADD COLUMN IF NOT EXISTS visibility VARCHAR(24) NOT NULL DEFAULT 'public';
ALTER TABLE notes ADD COLUMN IF NOT EXISTS content_version BIGINT NOT NULL DEFAULT 1;
ALTER TABLE notes ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

UPDATE notes SET project_id = 1 WHERE project_id = 0;
ALTER TABLE notes ALTER COLUMN project_id SET DEFAULT 1;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'notes_visibility_check') THEN
        ALTER TABLE notes ADD CONSTRAINT notes_visibility_check
            CHECK (visibility IN ('public', 'project'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'notes_project_fk') THEN
        ALTER TABLE notes ADD CONSTRAINT notes_project_fk
            FOREIGN KEY (project_id) REFERENCES projects(id);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_notes_project_visibility_created
    ON notes(project_id, visibility, created_at DESC, id DESC)
    WHERE status = 'published';

CREATE TABLE IF NOT EXISTS dataset_notes (
    dataset_id BIGINT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    note_id BIGINT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    note_version BIGINT NOT NULL DEFAULT 1,
    included_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (dataset_id, note_id)
);

INSERT INTO dataset_notes (dataset_id, note_id, note_version)
SELECT 1, id, content_version FROM notes
ON CONFLICT (dataset_id, note_id) DO UPDATE SET note_version = EXCLUDED.note_version;

CREATE OR REPLACE FUNCTION include_note_in_default_dataset()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.project_id = 1 THEN
        INSERT INTO dataset_notes (dataset_id, note_id, note_version)
        VALUES (1, NEW.id, NEW.content_version)
        ON CONFLICT (dataset_id, note_id) DO UPDATE SET note_version = EXCLUDED.note_version;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_notes_default_dataset ON notes;
CREATE TRIGGER trg_notes_default_dataset
AFTER INSERT OR UPDATE OF content_version ON notes
FOR EACH ROW EXECUTE FUNCTION include_note_in_default_dataset();

CREATE TABLE IF NOT EXISTS evidence_sources (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    dataset_id BIGINT REFERENCES datasets(id) ON DELETE SET NULL,
    source_type VARCHAR(40) NOT NULL,
    source_id BIGINT NOT NULL,
    source_version BIGINT NOT NULL DEFAULT 1,
    visibility VARCHAR(24) NOT NULL DEFAULT 'public'
        CHECK (visibility IN ('public', 'project')),
    content_hash VARCHAR(64) NOT NULL,
    index_status VARCHAR(24) NOT NULL DEFAULT 'pending'
        CHECK (index_status IN ('pending', 'indexed', 'failed', 'deleted')),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, source_type, source_id, source_version)
);

CREATE INDEX IF NOT EXISTS idx_evidence_sources_ingestion
    ON evidence_sources(project_id, dataset_id, index_status, updated_at, id);

ALTER TABLE behavior_events ALTER COLUMN project_id SET DEFAULT 1;
UPDATE behavior_events SET project_id = 1 WHERE project_id = 0;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'behavior_events_project_fk') THEN
        ALTER TABLE behavior_events ADD CONSTRAINT behavior_events_project_fk
            FOREIGN KEY (project_id) REFERENCES projects(id);
    END IF;
END $$;

ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS schema_version INT NOT NULL DEFAULT 1;
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS producer VARCHAR(80) NOT NULL DEFAULT 'noteinsight-api';
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS correlation_id VARCHAR(128);
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS trace_id VARCHAR(128);

ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ;
ALTER TABLE user_sessions ADD COLUMN IF NOT EXISTS replaced_by_session_id BIGINT REFERENCES user_sessions(id) ON DELETE SET NULL;

CREATE TABLE IF NOT EXISTS reconcile_state (
    state_key VARCHAR(80) PRIMARY KEY,
    last_id BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO reconcile_state (state_key, last_id)
VALUES ('notes', 0), ('comments', 0)
ON CONFLICT (state_key) DO NOTHING;

CREATE TABLE IF NOT EXISTS fact_materialization_runs (
    run_id VARCHAR(100) PRIMARY KEY,
    window_start TIMESTAMPTZ NOT NULL,
    window_end TIMESTAMPTZ NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'completed', 'failed')),
    note_fact_count BIGINT NOT NULL DEFAULT 0,
    user_fact_count BIGINT NOT NULL DEFAULT 0,
    error_message TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS note_daily_facts (
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    note_id BIGINT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    fact_date DATE NOT NULL,
    view_count BIGINT NOT NULL DEFAULT 0,
    like_count BIGINT NOT NULL DEFAULT 0,
    collect_count BIGINT NOT NULL DEFAULT 0,
    comment_count BIGINT NOT NULL DEFAULT 0,
    share_count BIGINT NOT NULL DEFAULT 0,
    unique_user_count BIGINT NOT NULL DEFAULT 0,
    event_count BIGINT NOT NULL DEFAULT 0,
    source_run_id VARCHAR(100) NOT NULL REFERENCES fact_materialization_runs(run_id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, note_id, fact_date)
);

CREATE INDEX IF NOT EXISTS idx_note_daily_facts_project_date
    ON note_daily_facts(project_id, fact_date DESC, note_id);

CREATE TABLE IF NOT EXISTS user_daily_facts (
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    fact_date DATE NOT NULL,
    view_count BIGINT NOT NULL DEFAULT 0,
    interaction_count BIGINT NOT NULL DEFAULT 0,
    content_count BIGINT NOT NULL DEFAULT 0,
    comment_count BIGINT NOT NULL DEFAULT 0,
    active_note_count BIGINT NOT NULL DEFAULT 0,
    event_count BIGINT NOT NULL DEFAULT 0,
    source_run_id VARCHAR(100) NOT NULL REFERENCES fact_materialization_runs(run_id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id, fact_date)
);

CREATE INDEX IF NOT EXISTS idx_user_daily_facts_project_date
    ON user_daily_facts(project_id, fact_date DESC, user_id);
