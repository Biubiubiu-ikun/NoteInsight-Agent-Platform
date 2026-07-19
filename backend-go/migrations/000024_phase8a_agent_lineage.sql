CREATE TABLE IF NOT EXISTS agent_prompt_versions (
    id BIGSERIAL PRIMARY KEY,
    prompt_key VARCHAR(80) NOT NULL,
    version VARCHAR(80) NOT NULL,
    purpose VARCHAR(80) NOT NULL,
    template_text TEXT NOT NULL,
    template_sha256 CHAR(64) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'frozen'
        CHECK (status IN ('frozen', 'retired')),
    created_by BIGINT REFERENCES users(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (prompt_key, version),
    UNIQUE (prompt_key, template_sha256),
    CHECK (char_length(template_text) > 0)
);

WITH seed(template_text) AS (
    VALUES ('You are the NoteInsight single insight agent. Use only authorized retrieved evidence. Every factual claim must cite exact source citations. Abstain when evidence is insufficient.'::TEXT)
)
INSERT INTO agent_prompt_versions (
    prompt_key, version, purpose, template_text, template_sha256, status
)
SELECT 'insight-agent-system', 'v1', 'insight_report', template_text,
       encode(digest(convert_to(template_text, 'UTF8'), 'sha256'), 'hex'), 'frozen'
FROM seed
ON CONFLICT (prompt_key, version) DO NOTHING;

CREATE TABLE IF NOT EXISTS agent_model_versions (
    id BIGSERIAL PRIMARY KEY,
    provider VARCHAR(80) NOT NULL,
    model VARCHAR(255) NOT NULL,
    revision VARCHAR(160) NOT NULL,
    parameters JSONB NOT NULL DEFAULT '{}'::jsonb,
    artifact_sha256 CHAR(64),
    status VARCHAR(24) NOT NULL DEFAULT 'frozen'
        CHECK (status IN ('frozen', 'retired')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, model, revision),
    CHECK (artifact_sha256 IS NULL OR char_length(artifact_sha256) = 64)
);

CREATE TABLE IF NOT EXISTS agent_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    dataset_version_id BIGINT NOT NULL REFERENCES dataset_versions(id) ON DELETE RESTRICT,
    ingestion_run_id VARCHAR(128) NOT NULL REFERENCES ingestion_runs(run_id) ON DELETE RESTRICT,
    requested_by BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    query TEXT NOT NULL,
    requested_mode VARCHAR(24) NOT NULL
        CHECK (requested_mode IN ('lexical', 'vector', 'hybrid')),
    intent JSONB NOT NULL DEFAULT '{}'::jsonb,
    retrieval_plan JSONB NOT NULL,
    report JSONB,
    prompt_version_id BIGINT NOT NULL REFERENCES agent_prompt_versions(id) ON DELETE RESTRICT,
    model_version_id BIGINT REFERENCES agent_model_versions(id) ON DELETE RESTRICT,
    status VARCHAR(24) NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    max_steps INTEGER NOT NULL CHECK (max_steps BETWEEN 1 AND 32),
    max_retrieval_calls INTEGER NOT NULL CHECK (max_retrieval_calls BETWEEN 1 AND 32),
    max_model_calls INTEGER NOT NULL CHECK (max_model_calls BETWEEN 1 AND 32),
    max_input_tokens INTEGER NOT NULL CHECK (max_input_tokens BETWEEN 1 AND 1000000),
    max_output_tokens INTEGER NOT NULL CHECK (max_output_tokens BETWEEN 1 AND 1000000),
    max_duration_ms BIGINT NOT NULL CHECK (max_duration_ms BETWEEN 1000 AND 3600000),
    max_cost_micros BIGINT NOT NULL CHECK (max_cost_micros BETWEEN 0 AND 1000000000),
    used_steps INTEGER NOT NULL DEFAULT 0 CHECK (used_steps >= 0 AND used_steps <= max_steps),
    used_retrieval_calls INTEGER NOT NULL DEFAULT 0 CHECK (used_retrieval_calls >= 0 AND used_retrieval_calls <= max_retrieval_calls),
    used_model_calls INTEGER NOT NULL DEFAULT 0 CHECK (used_model_calls >= 0 AND used_model_calls <= max_model_calls),
    used_input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (used_input_tokens >= 0),
    used_output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (used_output_tokens >= 0),
    used_cost_micros BIGINT NOT NULL DEFAULT 0 CHECK (used_cost_micros >= 0),
    cancellation_requested BOOLEAN NOT NULL DEFAULT FALSE,
    idempotency_key VARCHAR(128),
    request_hash CHAR(64) NOT NULL,
    request_id VARCHAR(128),
    trace_id VARCHAR(64),
    failure_code VARCHAR(80),
    failure_message TEXT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (char_length(query) BETWEEN 1 AND 2000),
    CHECK (idempotency_key IS NULL OR char_length(idempotency_key) BETWEEN 1 AND 128),
    CHECK (
        (status IN ('succeeded', 'failed', 'cancelled') AND completed_at IS NOT NULL)
        OR (status IN ('queued', 'running') AND completed_at IS NULL)
    ),
    CHECK (status <> 'succeeded' OR report IS NOT NULL)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_agent_runs_requester_idempotency
    ON agent_runs(requested_by, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_agent_runs_requester_history
    ON agent_runs(requested_by, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_agent_runs_dispatch
    ON agent_runs(status, created_at, id)
    WHERE status IN ('queued', 'running');

CREATE INDEX IF NOT EXISTS idx_agent_runs_scope
    ON agent_runs(project_id, dataset_version_id, ingestion_run_id, created_at DESC);

CREATE TABLE IF NOT EXISTS agent_tool_calls (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE RESTRICT,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt BETWEEN 1 AND 16),
    tool_name VARCHAR(120) NOT NULL,
    tool_version VARCHAR(120) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'cancelled')),
    input JSONB NOT NULL,
    output JSONB,
    output_sha256 CHAR(64),
    error_code VARCHAR(80),
    error_message TEXT,
    trace_id VARCHAR(64),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, sequence, attempt),
    CHECK (output_sha256 IS NULL OR char_length(output_sha256) = 64),
    CHECK (
        (status IN ('succeeded', 'failed', 'cancelled') AND completed_at IS NOT NULL)
        OR status IN ('queued', 'running')
    )
);

CREATE INDEX IF NOT EXISTS idx_agent_tool_calls_run
    ON agent_tool_calls(run_id, sequence, attempt);

CREATE TABLE IF NOT EXISTS agent_claims (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES agent_runs(id) ON DELETE RESTRICT,
    ordinal INTEGER NOT NULL CHECK (ordinal > 0),
    claim_type VARCHAR(40) NOT NULL DEFAULT 'factual',
    claim_text TEXT NOT NULL,
    support_status VARCHAR(24) NOT NULL DEFAULT 'proposed'
        CHECK (support_status IN ('proposed', 'supported', 'unsupported', 'conflicted')),
    confidence DOUBLE PRECISION NOT NULL DEFAULT 0
        CHECK (confidence >= 0 AND confidence <= 1),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, ordinal),
    CHECK (char_length(claim_text) > 0)
);

CREATE INDEX IF NOT EXISTS idx_agent_claims_run
    ON agent_claims(run_id, ordinal);

CREATE TABLE IF NOT EXISTS agent_claim_citations (
    claim_id UUID NOT NULL REFERENCES agent_claims(id) ON DELETE RESTRICT,
    source_citation_id BIGINT NOT NULL REFERENCES source_citations(id) ON DELETE RESTRICT,
    citation_order INTEGER NOT NULL CHECK (citation_order > 0),
    relationship VARCHAR(24) NOT NULL DEFAULT 'supports'
        CHECK (relationship IN ('supports', 'contradicts', 'context')),
    quote_hash_snapshot CHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (claim_id, source_citation_id),
    UNIQUE (claim_id, citation_order)
);

CREATE OR REPLACE FUNCTION guard_agent_definition_immutable()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION '% rows cannot be deleted', TG_TABLE_NAME;
    END IF;
    IF (to_jsonb(NEW) - 'status') IS DISTINCT FROM (to_jsonb(OLD) - 'status') THEN
        RAISE EXCEPTION '% definition fields are immutable', TG_TABLE_NAME;
    END IF;
    IF OLD.status = 'retired' AND NEW.status <> OLD.status THEN
        RAISE EXCEPTION 'retired % rows cannot be reactivated', TG_TABLE_NAME;
    END IF;
    IF OLD.status = 'frozen' AND NEW.status NOT IN ('frozen', 'retired') THEN
        RAISE EXCEPTION 'invalid % status transition', TG_TABLE_NAME;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_agent_prompt_version ON agent_prompt_versions;
CREATE TRIGGER trg_guard_agent_prompt_version
BEFORE UPDATE OR DELETE ON agent_prompt_versions
FOR EACH ROW EXECUTE FUNCTION guard_agent_definition_immutable();

DROP TRIGGER IF EXISTS trg_guard_agent_model_version ON agent_model_versions;
CREATE TRIGGER trg_guard_agent_model_version
BEFORE UPDATE OR DELETE ON agent_model_versions
FOR EACH ROW EXECUTE FUNCTION guard_agent_definition_immutable();

CREATE OR REPLACE FUNCTION validate_agent_run()
RETURNS TRIGGER AS $$
DECLARE
    source_project_id BIGINT;
    source_dataset_version_id BIGINT;
    source_status VARCHAR(24);
    prompt_status VARCHAR(24);
BEGIN
    SELECT project_id, dataset_version_id, status
      INTO source_project_id, source_dataset_version_id, source_status
      FROM ingestion_runs
     WHERE run_id = NEW.ingestion_run_id;
    IF NOT FOUND OR source_status <> 'completed'
       OR source_project_id <> NEW.project_id
       OR source_dataset_version_id <> NEW.dataset_version_id THEN
        RAISE EXCEPTION 'agent run retrieval scope is not a completed matching ingestion run';
    END IF;

    SELECT status INTO prompt_status
      FROM agent_prompt_versions
     WHERE id = NEW.prompt_version_id;
    IF NOT FOUND OR prompt_status <> 'frozen' THEN
        RAISE EXCEPTION 'agent run prompt version must be frozen';
    END IF;

    IF NEW.model_version_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM agent_model_versions
         WHERE id = NEW.model_version_id AND status = 'frozen'
    ) THEN
        RAISE EXCEPTION 'agent run model version must be frozen';
    END IF;

    IF TG_OP = 'INSERT' THEN
        IF NEW.status <> 'queued' THEN
            RAISE EXCEPTION 'new agent runs must be queued';
        END IF;
        RETURN NEW;
    END IF;

    IF OLD.status IN ('succeeded', 'failed', 'cancelled') THEN
        RAISE EXCEPTION 'terminal agent run % is immutable', OLD.id;
    END IF;
    IF ROW(
        NEW.id, NEW.project_id, NEW.dataset_version_id, NEW.ingestion_run_id,
        NEW.requested_by, NEW.query, NEW.requested_mode, NEW.retrieval_plan,
        NEW.prompt_version_id, NEW.max_steps,
        NEW.max_retrieval_calls, NEW.max_model_calls, NEW.max_input_tokens,
        NEW.max_output_tokens, NEW.max_duration_ms, NEW.max_cost_micros,
        NEW.idempotency_key, NEW.request_hash, NEW.request_id, NEW.trace_id,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.project_id, OLD.dataset_version_id, OLD.ingestion_run_id,
        OLD.requested_by, OLD.query, OLD.requested_mode, OLD.retrieval_plan,
        OLD.prompt_version_id, OLD.max_steps,
        OLD.max_retrieval_calls, OLD.max_model_calls, OLD.max_input_tokens,
        OLD.max_output_tokens, OLD.max_duration_ms, OLD.max_cost_micros,
        OLD.idempotency_key, OLD.request_hash, OLD.request_id, OLD.trace_id,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'agent run % immutable request lineage changed', OLD.id;
    END IF;
    IF NEW.model_version_id IS DISTINCT FROM OLD.model_version_id
       AND NOT (
           OLD.status = 'queued' AND NEW.status = 'running'
           AND OLD.model_version_id IS NULL AND NEW.model_version_id IS NOT NULL
       ) THEN
        RAISE EXCEPTION 'agent run % model lineage may only bind once at dispatch', OLD.id;
    END IF;
    IF NOT (
        NEW.status = OLD.status
        OR (OLD.status = 'queued' AND NEW.status IN ('running', 'failed', 'cancelled'))
        OR (OLD.status = 'running' AND NEW.status IN ('succeeded', 'failed', 'cancelled'))
    ) THEN
        RAISE EXCEPTION 'invalid agent run status transition % -> %', OLD.status, NEW.status;
    END IF;
    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_validate_agent_run ON agent_runs;
CREATE TRIGGER trg_validate_agent_run
BEFORE INSERT OR UPDATE ON agent_runs
FOR EACH ROW EXECUTE FUNCTION validate_agent_run();

CREATE OR REPLACE FUNCTION guard_agent_run_child()
RETURNS TRIGGER AS $$
DECLARE
    target_run_id UUID;
    run_status VARCHAR(24);
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION '% rows are immutable', TG_TABLE_NAME;
    END IF;
    target_run_id := CASE WHEN TG_TABLE_NAME = 'agent_tool_calls' THEN NEW.run_id ELSE NEW.run_id END;
    SELECT status INTO run_status FROM agent_runs WHERE id = target_run_id;
    IF NOT FOUND OR run_status IN ('succeeded', 'failed', 'cancelled') THEN
        RAISE EXCEPTION 'cannot mutate child lineage for terminal or missing agent run %', target_run_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_agent_tool_call ON agent_tool_calls;
CREATE TRIGGER trg_guard_agent_tool_call
BEFORE INSERT OR UPDATE OR DELETE ON agent_tool_calls
FOR EACH ROW EXECUTE FUNCTION guard_agent_run_child();

DROP TRIGGER IF EXISTS trg_guard_agent_claim ON agent_claims;
CREATE TRIGGER trg_guard_agent_claim
BEFORE INSERT OR UPDATE OR DELETE ON agent_claims
FOR EACH ROW EXECUTE FUNCTION guard_agent_run_child();

CREATE OR REPLACE FUNCTION validate_agent_claim_citation()
RETURNS TRIGGER AS $$
DECLARE
    run_project_id BIGINT;
    run_dataset_version_id BIGINT;
    run_status VARCHAR(24);
    citation_project_id BIGINT;
    citation_dataset_version_id BIGINT;
    citation_quote_hash CHAR(64);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'agent claim citation rows are immutable';
    END IF;
    SELECT r.project_id, r.dataset_version_id, r.status
      INTO run_project_id, run_dataset_version_id, run_status
      FROM agent_claims c
      JOIN agent_runs r ON r.id = c.run_id
     WHERE c.id = NEW.claim_id;
    IF NOT FOUND OR run_status IN ('succeeded', 'failed', 'cancelled') THEN
        RAISE EXCEPTION 'cannot mutate citation lineage for terminal or missing agent run';
    END IF;
    SELECT project_id, dataset_version_id, quote_hash
      INTO citation_project_id, citation_dataset_version_id, citation_quote_hash
      FROM source_citations
     WHERE id = NEW.source_citation_id;
    IF NOT FOUND
       OR citation_project_id <> run_project_id
       OR citation_dataset_version_id <> run_dataset_version_id THEN
        RAISE EXCEPTION 'agent claim citation is outside the run retrieval scope';
    END IF;
    IF NEW.quote_hash_snapshot <> citation_quote_hash THEN
        RAISE EXCEPTION 'agent claim citation quote hash does not match canonical citation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_validate_agent_claim_citation ON agent_claim_citations;
CREATE TRIGGER trg_validate_agent_claim_citation
BEFORE INSERT OR UPDATE OR DELETE ON agent_claim_citations
FOR EACH ROW EXECUTE FUNCTION validate_agent_claim_citation();
