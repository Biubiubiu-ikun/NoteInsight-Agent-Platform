CREATE TABLE IF NOT EXISTS retrieval_lexical_indexes (
    ingestion_run_id VARCHAR(128) PRIMARY KEY REFERENCES ingestion_runs(run_id) ON DELETE RESTRICT,
    dataset_version_id BIGINT NOT NULL REFERENCES dataset_versions(id) ON DELETE RESTRICT,
    tokenizer_version VARCHAR(80) NOT NULL,
    index_version VARCHAR(80) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'building'
        CHECK (status IN ('building', 'completed', 'failed')),
    document_count BIGINT NOT NULL DEFAULT 0,
    chunk_count BIGINT NOT NULL DEFAULT 0,
    lexeme_count BIGINT NOT NULL DEFAULT 0,
    index_checksum CHAR(64),
    error_message TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((status = 'completed' AND completed_at IS NOT NULL AND index_checksum IS NOT NULL)
        OR status <> 'completed')
);

CREATE TABLE IF NOT EXISTS retrieval_lexeme_stats (
    ingestion_run_id VARCHAR(128) NOT NULL REFERENCES retrieval_lexical_indexes(ingestion_run_id) ON DELETE RESTRICT,
    lexeme TEXT NOT NULL,
    chunk_frequency BIGINT NOT NULL CHECK (chunk_frequency > 0),
    occurrence_count BIGINT NOT NULL CHECK (occurrence_count >= chunk_frequency),
    inverse_document_frequency DOUBLE PRECISION NOT NULL CHECK (inverse_document_frequency > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (ingestion_run_id, lexeme)
);

CREATE INDEX IF NOT EXISTS idx_retrieval_lexeme_stats_frequency
    ON retrieval_lexeme_stats(ingestion_run_id, chunk_frequency, lexeme);

CREATE INDEX IF NOT EXISTS idx_ingestion_run_documents_document_run
    ON ingestion_run_documents(document_id, run_id);

CREATE TABLE IF NOT EXISTS retrieval_eval_runs (
    run_id VARCHAR(128) PRIMARY KEY,
    benchmark_id VARCHAR(128) NOT NULL REFERENCES retrieval_benchmarks(benchmark_id) ON DELETE RESTRICT,
    benchmark_manifest_checksum CHAR(64) NOT NULL,
    split VARCHAR(32) NOT NULL CHECK (split IN ('development', 'holdout')),
    release_id VARCHAR(128),
    ingestion_run_id VARCHAR(128) NOT NULL REFERENCES ingestion_runs(run_id) ON DELETE RESTRICT,
    dataset_version_id BIGINT NOT NULL REFERENCES dataset_versions(id) ON DELETE RESTRICT,
    retriever_version VARCHAR(80) NOT NULL,
    reranker_version VARCHAR(80) NOT NULL,
    metric_version VARCHAR(80) NOT NULL,
    config JSONB NOT NULL,
    config_checksum CHAR(64) NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'running'
        CHECK (status IN ('running', 'completed', 'failed')),
    case_count INT NOT NULL DEFAULT 0,
    metrics JSONB,
    failure_counts JSONB,
    error_message TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (split <> 'holdout' OR release_id IS NOT NULL),
    CHECK ((status = 'completed' AND completed_at IS NOT NULL AND metrics IS NOT NULL)
        OR status <> 'completed')
);

CREATE INDEX IF NOT EXISTS idx_retrieval_eval_runs_benchmark
    ON retrieval_eval_runs(benchmark_id, split, completed_at DESC, run_id);

CREATE TABLE IF NOT EXISTS retrieval_eval_case_results (
    eval_run_id VARCHAR(128) NOT NULL REFERENCES retrieval_eval_runs(run_id) ON DELETE RESTRICT,
    case_checksum CHAR(64) NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    answerable BOOLEAN NOT NULL,
    gold_sources JSONB NOT NULL,
    retrieved_sources JSONB NOT NULL,
    metrics JSONB NOT NULL,
    failure_category VARCHAR(80),
    latency_ms DOUBLE PRECISION NOT NULL CHECK (latency_ms >= 0),
    result_count INT NOT NULL CHECK (result_count >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (eval_run_id, case_checksum)
);

CREATE INDEX IF NOT EXISTS idx_retrieval_eval_cases_failure
    ON retrieval_eval_case_results(eval_run_id, task_type, failure_category);

CREATE OR REPLACE FUNCTION guard_completed_retrieval_lexical_index()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'completed' THEN
        RAISE EXCEPTION 'completed retrieval lexical index % is immutable', OLD.ingestion_run_id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_completed_retrieval_lexical_index ON retrieval_lexical_indexes;
CREATE TRIGGER trg_guard_completed_retrieval_lexical_index
BEFORE UPDATE OR DELETE ON retrieval_lexical_indexes
FOR EACH ROW EXECUTE FUNCTION guard_completed_retrieval_lexical_index();

CREATE OR REPLACE FUNCTION guard_retrieval_lexeme_stats()
RETURNS TRIGGER AS $$
DECLARE
    target_run_id VARCHAR(128);
    target_status VARCHAR(24);
BEGIN
    target_run_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.ingestion_run_id ELSE NEW.ingestion_run_id END;
    SELECT status INTO target_status FROM retrieval_lexical_indexes WHERE ingestion_run_id = target_run_id;
    IF target_status = 'completed' THEN
        RAISE EXCEPTION 'lexeme stats for completed index % are immutable', target_run_id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_retrieval_lexeme_stats ON retrieval_lexeme_stats;
CREATE TRIGGER trg_guard_retrieval_lexeme_stats
BEFORE INSERT OR UPDATE OR DELETE ON retrieval_lexeme_stats
FOR EACH ROW EXECUTE FUNCTION guard_retrieval_lexeme_stats();

CREATE OR REPLACE FUNCTION guard_completed_retrieval_eval_run()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'completed' THEN
        RAISE EXCEPTION 'completed retrieval evaluation run % is immutable', OLD.run_id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_completed_retrieval_eval_run ON retrieval_eval_runs;
CREATE TRIGGER trg_guard_completed_retrieval_eval_run
BEFORE UPDATE OR DELETE ON retrieval_eval_runs
FOR EACH ROW EXECUTE FUNCTION guard_completed_retrieval_eval_run();

CREATE OR REPLACE FUNCTION guard_retrieval_eval_case_result()
RETURNS TRIGGER AS $$
DECLARE
    target_run_id VARCHAR(128);
    target_status VARCHAR(24);
BEGIN
    target_run_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.eval_run_id ELSE NEW.eval_run_id END;
    SELECT status INTO target_status FROM retrieval_eval_runs WHERE run_id = target_run_id;
    IF target_status = 'completed' THEN
        RAISE EXCEPTION 'case results for completed evaluation run % are immutable', target_run_id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_retrieval_eval_case_result ON retrieval_eval_case_results;
CREATE TRIGGER trg_guard_retrieval_eval_case_result
BEFORE INSERT OR UPDATE OR DELETE ON retrieval_eval_case_results
FOR EACH ROW EXECUTE FUNCTION guard_retrieval_eval_case_result();
