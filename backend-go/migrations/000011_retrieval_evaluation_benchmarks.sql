CREATE TABLE IF NOT EXISTS retrieval_benchmarks (
    benchmark_id VARCHAR(128) PRIMARY KEY,
    benchmark_version VARCHAR(64) NOT NULL,
    source_run_id VARCHAR(128) NOT NULL REFERENCES content_corpus_runs(run_id) ON DELETE RESTRICT,
    generator_version VARCHAR(64) NOT NULL,
    seed BIGINT NOT NULL,
    split_policy JSONB NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'building',
    case_count INT NOT NULL DEFAULT 0,
    manifest_checksum CHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    frozen_at TIMESTAMPTZ,
    CHECK (status IN ('building', 'frozen')),
    CHECK ((status = 'building' AND frozen_at IS NULL) OR
           (status = 'frozen' AND frozen_at IS NOT NULL AND manifest_checksum IS NOT NULL))
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_retrieval_benchmark_version
    ON retrieval_benchmarks(benchmark_version);

CREATE TABLE IF NOT EXISTS retrieval_benchmark_cases (
    id BIGSERIAL PRIMARY KEY,
    benchmark_id VARCHAR(128) NOT NULL REFERENCES retrieval_benchmarks(benchmark_id) ON DELETE RESTRICT,
    split VARCHAR(32) NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    query TEXT NOT NULL,
    expected_answer TEXT NOT NULL,
    gold_sources JSONB NOT NULL DEFAULT '[]'::jsonb,
    adversarial_tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    provenance VARCHAR(64) NOT NULL,
    review_status VARCHAR(32) NOT NULL,
    case_checksum CHAR(64) NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (split IN ('development', 'holdout')),
    CHECK (review_status IN ('machine_validated', 'human_approved')),
    UNIQUE (benchmark_id, case_checksum)
);

CREATE INDEX IF NOT EXISTS idx_retrieval_benchmark_cases_split_task
    ON retrieval_benchmark_cases(benchmark_id, split, task_type, id);

CREATE OR REPLACE FUNCTION guard_frozen_retrieval_benchmark()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'frozen' THEN
        RAISE EXCEPTION 'frozen retrieval benchmark % is immutable', OLD.benchmark_id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_frozen_retrieval_benchmark ON retrieval_benchmarks;
CREATE TRIGGER trg_guard_frozen_retrieval_benchmark
BEFORE UPDATE OR DELETE ON retrieval_benchmarks
FOR EACH ROW EXECUTE FUNCTION guard_frozen_retrieval_benchmark();

CREATE OR REPLACE FUNCTION guard_frozen_retrieval_benchmark_case()
RETURNS TRIGGER AS $$
DECLARE
    target_benchmark_id VARCHAR(128);
    target_status VARCHAR(32);
BEGIN
    IF TG_OP = 'DELETE' THEN
        target_benchmark_id := OLD.benchmark_id;
    ELSE
        target_benchmark_id := NEW.benchmark_id;
    END IF;
    SELECT status INTO target_status
    FROM retrieval_benchmarks
    WHERE benchmark_id = target_benchmark_id
    FOR SHARE;

    IF target_status = 'frozen' THEN
        RAISE EXCEPTION 'cases for frozen retrieval benchmark % are immutable', target_benchmark_id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_frozen_retrieval_benchmark_case ON retrieval_benchmark_cases;
CREATE TRIGGER trg_guard_frozen_retrieval_benchmark_case
BEFORE INSERT OR UPDATE OR DELETE ON retrieval_benchmark_cases
FOR EACH ROW EXECUTE FUNCTION guard_frozen_retrieval_benchmark_case();
