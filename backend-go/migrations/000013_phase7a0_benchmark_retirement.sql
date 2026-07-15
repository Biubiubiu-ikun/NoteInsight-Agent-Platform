ALTER TABLE retrieval_benchmarks
    ADD COLUMN IF NOT EXISTS dataset_version_id BIGINT REFERENCES dataset_versions(id) ON DELETE RESTRICT,
    ADD COLUMN IF NOT EXISTS commitment_scheme VARCHAR(64) NOT NULL DEFAULT 'legacy_case_checksum_v1',
    ADD COLUMN IF NOT EXISTS retired_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS retired_reason TEXT;

ALTER TABLE retrieval_benchmark_cases
    ADD COLUMN IF NOT EXISTS commitment_hash CHAR(64);

ALTER TABLE retrieval_benchmarks DROP CONSTRAINT IF EXISTS retrieval_benchmarks_status_check;
ALTER TABLE retrieval_benchmarks DROP CONSTRAINT IF EXISTS retrieval_benchmarks_check;
ALTER TABLE retrieval_benchmarks ADD CONSTRAINT retrieval_benchmarks_status_check
    CHECK (status IN ('building', 'frozen', 'retired'));
ALTER TABLE retrieval_benchmarks ADD CONSTRAINT retrieval_benchmarks_lifecycle_check
    CHECK (
        (status = 'building' AND frozen_at IS NULL AND retired_at IS NULL)
        OR
        (status = 'frozen' AND frozen_at IS NOT NULL AND manifest_checksum IS NOT NULL AND retired_at IS NULL)
        OR
        (status = 'retired' AND frozen_at IS NOT NULL AND manifest_checksum IS NOT NULL AND retired_at IS NOT NULL AND retired_reason IS NOT NULL)
    );

DROP TRIGGER IF EXISTS trg_guard_frozen_retrieval_benchmark ON retrieval_benchmarks;

UPDATE retrieval_benchmarks
SET status = 'retired',
    retired_at = now(),
    retired_reason = CASE benchmark_id
        WHEN 'retrieval_v3_20260715' THEN 'Retired before Phase 7 because public deterministic inputs reproduce every case commitment.'
        ELSE 'Retired historical benchmark; retained for audit only and not approved for retrieval quality claims.'
    END
WHERE benchmark_id IN ('retrieval_v1_20260715', 'retrieval_v2_20260715', 'retrieval_v3_20260715')
  AND status = 'frozen';

CREATE OR REPLACE FUNCTION guard_frozen_retrieval_benchmark()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status IN ('frozen', 'retired') THEN
        RAISE EXCEPTION 'immutable retrieval benchmark % cannot be changed', OLD.benchmark_id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_guard_frozen_retrieval_benchmark
BEFORE UPDATE OR DELETE ON retrieval_benchmarks
FOR EACH ROW EXECUTE FUNCTION guard_frozen_retrieval_benchmark();

CREATE OR REPLACE FUNCTION guard_frozen_retrieval_benchmark_case()
RETURNS TRIGGER AS $$
DECLARE
    target_benchmark_id VARCHAR(128);
    target_status VARCHAR(32);
BEGIN
    target_benchmark_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.benchmark_id ELSE NEW.benchmark_id END;
    SELECT status INTO target_status
    FROM retrieval_benchmarks
    WHERE benchmark_id = target_benchmark_id
    FOR SHARE;

    IF target_status IN ('frozen', 'retired') THEN
        RAISE EXCEPTION 'cases for immutable retrieval benchmark % cannot be changed', target_benchmark_id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE UNIQUE INDEX IF NOT EXISTS uq_retrieval_benchmark_commitment
    ON retrieval_benchmark_cases(benchmark_id, commitment_hash)
    WHERE commitment_hash IS NOT NULL;
