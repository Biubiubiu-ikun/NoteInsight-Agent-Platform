CREATE EXTENSION IF NOT EXISTS pg_trgm;

ALTER TABLE note_daily_facts
    ADD COLUMN IF NOT EXISTS content_version BIGINT NOT NULL DEFAULT 1;

ALTER TABLE user_daily_facts
    ADD COLUMN IF NOT EXISTS content_version BIGINT NOT NULL DEFAULT 1;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'note_daily_facts_content_version_check') THEN
        ALTER TABLE note_daily_facts ADD CONSTRAINT note_daily_facts_content_version_check
            CHECK (content_version > 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'user_daily_facts_content_version_check') THEN
        ALTER TABLE user_daily_facts ADD CONSTRAINT user_daily_facts_content_version_check
            CHECK (content_version > 0);
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS daily_fact_payloads (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    fact_type VARCHAR(32) NOT NULL CHECK (fact_type IN ('note_daily_fact', 'user_daily_fact')),
    subject_id BIGINT NOT NULL,
    fact_date DATE NOT NULL,
    source_version BIGINT NOT NULL CHECK (source_version > 0),
    source_run_id VARCHAR(100) NOT NULL REFERENCES fact_materialization_runs(run_id) ON DELETE RESTRICT,
    source_payload JSONB NOT NULL,
    payload_hash CHAR(64) NOT NULL,
    payload_schema VARCHAR(48) NOT NULL DEFAULT 'daily_fact_payload_v1',
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, fact_type, subject_id, fact_date, source_version)
);

CREATE INDEX IF NOT EXISTS idx_daily_fact_payloads_latest
    ON daily_fact_payloads(project_id, fact_type, subject_id, fact_date, source_version DESC);

CREATE OR REPLACE FUNCTION enforce_note_daily_fact_content_version()
RETURNS TRIGGER AS $$
BEGIN
    IF ROW(
        NEW.view_count, NEW.like_count, NEW.collect_count, NEW.comment_count,
        NEW.share_count, NEW.unique_user_count, NEW.event_count
    ) IS DISTINCT FROM ROW(
        OLD.view_count, OLD.like_count, OLD.collect_count, OLD.comment_count,
        OLD.share_count, OLD.unique_user_count, OLD.event_count
    ) THEN
        NEW.content_version := OLD.content_version + 1;
    ELSE
        NEW.content_version := OLD.content_version;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_note_daily_facts_content_version ON note_daily_facts;
CREATE TRIGGER trg_note_daily_facts_content_version
BEFORE UPDATE ON note_daily_facts
FOR EACH ROW EXECUTE FUNCTION enforce_note_daily_fact_content_version();

CREATE OR REPLACE FUNCTION enforce_user_daily_fact_content_version()
RETURNS TRIGGER AS $$
BEGIN
    IF ROW(
        NEW.view_count, NEW.interaction_count, NEW.content_count, NEW.comment_count,
        NEW.active_note_count, NEW.event_count
    ) IS DISTINCT FROM ROW(
        OLD.view_count, OLD.interaction_count, OLD.content_count, OLD.comment_count,
        OLD.active_note_count, OLD.event_count
    ) THEN
        NEW.content_version := OLD.content_version + 1;
    ELSE
        NEW.content_version := OLD.content_version;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_user_daily_facts_content_version ON user_daily_facts;
CREATE TRIGGER trg_user_daily_facts_content_version
BEFORE UPDATE ON user_daily_facts
FOR EACH ROW EXECUTE FUNCTION enforce_user_daily_fact_content_version();

CREATE OR REPLACE FUNCTION capture_note_daily_fact_payload()
RETURNS TRIGGER AS $$
DECLARE
    captured JSONB;
BEGIN
    captured := jsonb_build_object(
        'project_id', NEW.project_id, 'note_id', NEW.note_id, 'fact_date', NEW.fact_date,
        'view_count', NEW.view_count, 'like_count', NEW.like_count,
        'collect_count', NEW.collect_count, 'comment_count', NEW.comment_count,
        'share_count', NEW.share_count, 'unique_user_count', NEW.unique_user_count,
        'event_count', NEW.event_count, 'source_run_id', NEW.source_run_id
    );
    INSERT INTO daily_fact_payloads (
        project_id, fact_type, subject_id, fact_date, source_version,
        source_run_id, source_payload, payload_hash
    ) VALUES (
        NEW.project_id, 'note_daily_fact', NEW.note_id, NEW.fact_date, NEW.content_version,
        NEW.source_run_id, captured,
        encode(digest(convert_to(captured::text, 'UTF8'), 'sha256'), 'hex')
    ) ON CONFLICT (project_id, fact_type, subject_id, fact_date, source_version) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_capture_note_daily_fact_payload ON note_daily_facts;
CREATE TRIGGER trg_capture_note_daily_fact_payload
AFTER INSERT OR UPDATE ON note_daily_facts
FOR EACH ROW EXECUTE FUNCTION capture_note_daily_fact_payload();

CREATE OR REPLACE FUNCTION capture_user_daily_fact_payload()
RETURNS TRIGGER AS $$
DECLARE
    captured JSONB;
BEGIN
    captured := jsonb_build_object(
        'project_id', NEW.project_id, 'user_id', NEW.user_id, 'fact_date', NEW.fact_date,
        'view_count', NEW.view_count, 'interaction_count', NEW.interaction_count,
        'content_count', NEW.content_count, 'comment_count', NEW.comment_count,
        'active_note_count', NEW.active_note_count, 'event_count', NEW.event_count,
        'source_run_id', NEW.source_run_id
    );
    INSERT INTO daily_fact_payloads (
        project_id, fact_type, subject_id, fact_date, source_version,
        source_run_id, source_payload, payload_hash
    ) VALUES (
        NEW.project_id, 'user_daily_fact', NEW.user_id, NEW.fact_date, NEW.content_version,
        NEW.source_run_id, captured,
        encode(digest(convert_to(captured::text, 'UTF8'), 'sha256'), 'hex')
    ) ON CONFLICT (project_id, fact_type, subject_id, fact_date, source_version) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_capture_user_daily_fact_payload ON user_daily_facts;
CREATE TRIGGER trg_capture_user_daily_fact_payload
AFTER INSERT OR UPDATE ON user_daily_facts
FOR EACH ROW EXECUTE FUNCTION capture_user_daily_fact_payload();

INSERT INTO daily_fact_payloads (
    project_id, fact_type, subject_id, fact_date, source_version,
    source_run_id, source_payload, payload_hash
)
SELECT project_id, 'note_daily_fact', note_id, fact_date, content_version, source_run_id,
       payload,
       encode(digest(convert_to(payload::text, 'UTF8'), 'sha256'), 'hex')
FROM note_daily_facts
CROSS JOIN LATERAL (
    SELECT jsonb_build_object(
        'project_id', project_id, 'note_id', note_id, 'fact_date', fact_date,
        'view_count', view_count, 'like_count', like_count,
        'collect_count', collect_count, 'comment_count', comment_count,
        'share_count', share_count, 'unique_user_count', unique_user_count,
        'event_count', event_count, 'source_run_id', source_run_id
    ) AS payload
) captured
ON CONFLICT (project_id, fact_type, subject_id, fact_date, source_version) DO NOTHING;

INSERT INTO daily_fact_payloads (
    project_id, fact_type, subject_id, fact_date, source_version,
    source_run_id, source_payload, payload_hash
)
SELECT project_id, 'user_daily_fact', user_id, fact_date, content_version, source_run_id,
       payload,
       encode(digest(convert_to(payload::text, 'UTF8'), 'sha256'), 'hex')
FROM user_daily_facts
CROSS JOIN LATERAL (
    SELECT jsonb_build_object(
        'project_id', project_id, 'user_id', user_id, 'fact_date', fact_date,
        'view_count', view_count, 'interaction_count', interaction_count,
        'content_count', content_count, 'comment_count', comment_count,
        'active_note_count', active_note_count, 'event_count', event_count,
        'source_run_id', source_run_id
    ) AS payload
) captured
ON CONFLICT (project_id, fact_type, subject_id, fact_date, source_version) DO NOTHING;

CREATE OR REPLACE FUNCTION guard_daily_fact_payload()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'daily fact payload % is immutable', OLD.id;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_daily_fact_payload ON daily_fact_payloads;
CREATE TRIGGER trg_guard_daily_fact_payload
BEFORE UPDATE OR DELETE ON daily_fact_payloads
FOR EACH ROW EXECUTE FUNCTION guard_daily_fact_payload();

CREATE TABLE IF NOT EXISTS ingestion_runs (
    run_id VARCHAR(128) PRIMARY KEY,
    dataset_version_id BIGINT NOT NULL REFERENCES dataset_versions(id) ON DELETE RESTRICT,
    dataset_id BIGINT NOT NULL REFERENCES datasets(id) ON DELETE RESTRICT,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    mode VARCHAR(24) NOT NULL CHECK (mode IN ('incremental', 'rebuild')),
    parser_version VARCHAR(80) NOT NULL,
    chunker_version VARCHAR(80) NOT NULL,
    tokenizer_version VARCHAR(80) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'completed', 'failed')),
    dataset_manifest_checksum CHAR(64) NOT NULL,
    input_checksum CHAR(64),
    output_checksum CHAR(64),
    source_count BIGINT NOT NULL DEFAULT 0,
    fact_source_count BIGINT NOT NULL DEFAULT 0,
    document_count BIGINT NOT NULL DEFAULT 0,
    chunk_count BIGINT NOT NULL DEFAULT 0,
    citation_count BIGINT NOT NULL DEFAULT 0,
    reused_document_count BIGINT NOT NULL DEFAULT 0,
    error_message TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status = 'completed' AND completed_at IS NOT NULL AND output_checksum IS NOT NULL)
        OR status <> 'completed')
);

CREATE INDEX IF NOT EXISTS idx_ingestion_runs_dataset_version
    ON ingestion_runs(dataset_version_id, completed_at DESC, run_id);

CREATE TABLE IF NOT EXISTS ingestion_run_fact_inputs (
    run_id VARCHAR(128) NOT NULL REFERENCES ingestion_runs(run_id) ON DELETE RESTRICT,
    daily_fact_payload_id BIGINT NOT NULL REFERENCES daily_fact_payloads(id) ON DELETE RESTRICT,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    fact_type VARCHAR(32) NOT NULL,
    subject_id BIGINT NOT NULL,
    fact_date DATE NOT NULL,
    source_version BIGINT NOT NULL,
    content_hash CHAR(64) NOT NULL,
    PRIMARY KEY (run_id, daily_fact_payload_id),
    UNIQUE (run_id, fact_type, subject_id, fact_date)
);

CREATE TABLE IF NOT EXISTS evidence_documents (
    id BIGSERIAL PRIMARY KEY,
    document_key CHAR(64) NOT NULL UNIQUE,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    dataset_id BIGINT NOT NULL REFERENCES datasets(id) ON DELETE RESTRICT,
    dataset_version_id BIGINT NOT NULL REFERENCES dataset_versions(id) ON DELETE RESTRICT,
    document_type VARCHAR(40) NOT NULL,
    source_type VARCHAR(40) NOT NULL,
    source_id BIGINT,
    source_key VARCHAR(192) NOT NULL,
    source_version BIGINT NOT NULL CHECK (source_version > 0),
    source_content_hash CHAR(64) NOT NULL,
    parser_version VARCHAR(80) NOT NULL,
    visibility VARCHAR(24) NOT NULL CHECK (visibility IN ('public', 'project')),
    canonical_text TEXT NOT NULL,
    content_hash CHAR(64) NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    expected_chunk_count INT NOT NULL CHECK (expected_chunk_count > 0),
    lifecycle_status VARCHAR(24) NOT NULL DEFAULT 'building'
        CHECK (lifecycle_status IN ('building', 'ready', 'stale', 'superseded', 'deleted', 'failed')),
    source_created_at TIMESTAMPTZ,
    source_updated_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (
        dataset_version_id, document_type, source_key, source_version,
        source_content_hash, parser_version
    )
);

CREATE INDEX IF NOT EXISTS idx_evidence_documents_scope
    ON evidence_documents(project_id, dataset_id, dataset_version_id, lifecycle_status, id);

CREATE INDEX IF NOT EXISTS idx_evidence_documents_source
    ON evidence_documents(project_id, source_type, source_key, source_version DESC);

CREATE TABLE IF NOT EXISTS evidence_document_sources (
    document_id BIGINT NOT NULL REFERENCES evidence_documents(id) ON DELETE RESTRICT,
    evidence_source_id BIGINT REFERENCES evidence_sources(id) ON DELETE RESTRICT,
    daily_fact_payload_id BIGINT REFERENCES daily_fact_payloads(id) ON DELETE RESTRICT,
    source_type VARCHAR(40) NOT NULL,
    source_id BIGINT NOT NULL,
    source_version BIGINT NOT NULL,
    content_hash CHAR(64) NOT NULL,
    source_order INT NOT NULL CHECK (source_order >= 0),
    PRIMARY KEY (document_id, source_order),
    CHECK ((evidence_source_id IS NOT NULL)::int + (daily_fact_payload_id IS NOT NULL)::int = 1)
);

CREATE INDEX IF NOT EXISTS idx_evidence_document_sources_registry
    ON evidence_document_sources(evidence_source_id) WHERE evidence_source_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_evidence_document_sources_fact
    ON evidence_document_sources(daily_fact_payload_id) WHERE daily_fact_payload_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS evidence_chunks (
    id BIGSERIAL PRIMARY KEY,
    chunk_key CHAR(64) NOT NULL UNIQUE,
    document_id BIGINT NOT NULL REFERENCES evidence_documents(id) ON DELETE RESTRICT,
    chunk_index INT NOT NULL CHECK (chunk_index >= 0),
    start_byte INT NOT NULL CHECK (start_byte >= 0),
    end_byte INT NOT NULL CHECK (end_byte > start_byte),
    start_rune INT NOT NULL CHECK (start_rune >= 0),
    end_rune INT NOT NULL CHECK (end_rune > start_rune),
    content TEXT NOT NULL,
    content_hash CHAR(64) NOT NULL,
    chunker_version VARCHAR(80) NOT NULL,
    tokenizer_version VARCHAR(80) NOT NULL,
    lexemes TEXT NOT NULL,
    search_vector TSVECTOR GENERATED ALWAYS AS (to_tsvector('simple', lexemes)) STORED,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (document_id, chunk_index)
);

CREATE INDEX IF NOT EXISTS idx_evidence_chunks_document
    ON evidence_chunks(document_id, chunk_index);

CREATE INDEX IF NOT EXISTS idx_evidence_chunks_fts
    ON evidence_chunks USING GIN(search_vector);

CREATE INDEX IF NOT EXISTS idx_evidence_chunks_trgm
    ON evidence_chunks USING GIN(content gin_trgm_ops);

CREATE TABLE IF NOT EXISTS source_citations (
    id BIGSERIAL PRIMARY KEY,
    citation_key CHAR(64) NOT NULL UNIQUE,
    document_id BIGINT NOT NULL REFERENCES evidence_documents(id) ON DELETE RESTRICT,
    chunk_id BIGINT NOT NULL REFERENCES evidence_chunks(id) ON DELETE RESTRICT,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    dataset_id BIGINT NOT NULL REFERENCES datasets(id) ON DELETE RESTRICT,
    dataset_version_id BIGINT NOT NULL REFERENCES dataset_versions(id) ON DELETE RESTRICT,
    evidence_source_id BIGINT REFERENCES evidence_sources(id) ON DELETE RESTRICT,
    daily_fact_payload_id BIGINT REFERENCES daily_fact_payloads(id) ON DELETE RESTRICT,
    source_type VARCHAR(40) NOT NULL,
    source_id BIGINT NOT NULL,
    source_version BIGINT NOT NULL,
    source_content_hash CHAR(64) NOT NULL,
    parser_version VARCHAR(80) NOT NULL,
    document_start_byte INT NOT NULL,
    document_end_byte INT NOT NULL,
    source_start_byte INT NOT NULL,
    source_end_byte INT NOT NULL,
    quote_hash CHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (document_start_byte >= 0 AND document_end_byte > document_start_byte),
    CHECK (source_start_byte >= 0 AND source_end_byte > source_start_byte),
    CHECK ((evidence_source_id IS NOT NULL)::int + (daily_fact_payload_id IS NOT NULL)::int = 1),
    UNIQUE (chunk_id, citation_key)
);

CREATE INDEX IF NOT EXISTS idx_source_citations_source
    ON source_citations(dataset_version_id, source_type, source_id, source_version);

CREATE INDEX IF NOT EXISTS idx_source_citations_chunk
    ON source_citations(chunk_id, document_start_byte);

CREATE TABLE IF NOT EXISTS ingestion_run_documents (
    run_id VARCHAR(128) NOT NULL REFERENCES ingestion_runs(run_id) ON DELETE RESTRICT,
    document_id BIGINT NOT NULL REFERENCES evidence_documents(id) ON DELETE RESTRICT,
    disposition VARCHAR(16) NOT NULL CHECK (disposition IN ('created', 'reused')),
    linked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, document_id)
);

CREATE INDEX IF NOT EXISTS idx_ingestion_run_documents_document
    ON ingestion_run_documents(document_id, run_id);

CREATE OR REPLACE FUNCTION guard_evidence_document_identity()
RETURNS TRIGGER AS $$
BEGIN
    IF ROW(
        NEW.document_key, NEW.project_id, NEW.dataset_id, NEW.dataset_version_id,
        NEW.document_type, NEW.source_type, NEW.source_id, NEW.source_key,
        NEW.source_version, NEW.source_content_hash, NEW.parser_version,
        NEW.visibility, NEW.canonical_text, NEW.content_hash, NEW.metadata,
        NEW.expected_chunk_count, NEW.source_created_at, NEW.source_updated_at
    ) IS DISTINCT FROM ROW(
        OLD.document_key, OLD.project_id, OLD.dataset_id, OLD.dataset_version_id,
        OLD.document_type, OLD.source_type, OLD.source_id, OLD.source_key,
        OLD.source_version, OLD.source_content_hash, OLD.parser_version,
        OLD.visibility, OLD.canonical_text, OLD.content_hash, OLD.metadata,
        OLD.expected_chunk_count, OLD.source_created_at, OLD.source_updated_at
    ) THEN
        RAISE EXCEPTION 'evidence document % identity and canonical content are immutable', OLD.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_evidence_document_identity ON evidence_documents;
CREATE TRIGGER trg_guard_evidence_document_identity
BEFORE UPDATE ON evidence_documents
FOR EACH ROW EXECUTE FUNCTION guard_evidence_document_identity();

CREATE OR REPLACE FUNCTION guard_immutable_evidence_child()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION '% row is immutable', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_evidence_document_source ON evidence_document_sources;
CREATE TRIGGER trg_guard_evidence_document_source
BEFORE UPDATE OR DELETE ON evidence_document_sources
FOR EACH ROW EXECUTE FUNCTION guard_immutable_evidence_child();

DROP TRIGGER IF EXISTS trg_guard_evidence_chunk ON evidence_chunks;
CREATE TRIGGER trg_guard_evidence_chunk
BEFORE UPDATE OR DELETE ON evidence_chunks
FOR EACH ROW EXECUTE FUNCTION guard_immutable_evidence_child();

DROP TRIGGER IF EXISTS trg_guard_source_citation ON source_citations;
CREATE TRIGGER trg_guard_source_citation
BEFORE UPDATE OR DELETE ON source_citations
FOR EACH ROW EXECUTE FUNCTION guard_immutable_evidence_child();

CREATE OR REPLACE FUNCTION guard_completed_ingestion_run()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'completed' THEN
        RAISE EXCEPTION 'completed ingestion run % is immutable', OLD.run_id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_completed_ingestion_run ON ingestion_runs;
CREATE TRIGGER trg_guard_completed_ingestion_run
BEFORE UPDATE OR DELETE ON ingestion_runs
FOR EACH ROW EXECUTE FUNCTION guard_completed_ingestion_run();

CREATE OR REPLACE FUNCTION guard_completed_ingestion_child()
RETURNS TRIGGER AS $$
DECLARE
    target_run_id VARCHAR(128);
    target_status VARCHAR(24);
BEGIN
    target_run_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.run_id ELSE NEW.run_id END;
    SELECT status INTO target_status FROM ingestion_runs WHERE run_id=target_run_id;
    IF target_status = 'completed' THEN
        RAISE EXCEPTION 'children of completed ingestion run % are immutable', target_run_id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_completed_fact_input ON ingestion_run_fact_inputs;
CREATE TRIGGER trg_guard_completed_fact_input
BEFORE INSERT OR UPDATE OR DELETE ON ingestion_run_fact_inputs
FOR EACH ROW EXECUTE FUNCTION guard_completed_ingestion_child();

DROP TRIGGER IF EXISTS trg_guard_completed_run_document ON ingestion_run_documents;
CREATE TRIGGER trg_guard_completed_run_document
BEFORE INSERT OR UPDATE OR DELETE ON ingestion_run_documents
FOR EACH ROW EXECUTE FUNCTION guard_completed_ingestion_child();
