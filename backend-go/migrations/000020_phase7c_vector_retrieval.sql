CREATE TABLE IF NOT EXISTS retrieval_vector_indexes (
    ingestion_run_id VARCHAR(128) NOT NULL REFERENCES ingestion_runs(run_id) ON DELETE RESTRICT,
    index_version VARCHAR(80) NOT NULL,
    embedding_model VARCHAR(255) NOT NULL,
    embedding_revision VARCHAR(128) NOT NULL,
    vector_dimension INTEGER NOT NULL CHECK (vector_dimension > 0),
    distance_metric VARCHAR(24) NOT NULL CHECK (distance_metric IN ('Cosine')),
    collection_name VARCHAR(255) NOT NULL UNIQUE,
    status VARCHAR(24) NOT NULL DEFAULT 'building'
        CHECK (status IN ('building', 'completed', 'failed')),
    point_count BIGINT NOT NULL DEFAULT 0 CHECK (point_count >= 0),
    index_checksum CHAR(64),
    error_message TEXT,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (ingestion_run_id, index_version),
    CHECK ((status = 'completed' AND completed_at IS NOT NULL AND index_checksum IS NOT NULL)
        OR status <> 'completed')
);

CREATE INDEX IF NOT EXISTS idx_retrieval_vector_indexes_status
    ON retrieval_vector_indexes(status, completed_at DESC, ingestion_run_id);

CREATE OR REPLACE FUNCTION guard_completed_retrieval_vector_index()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'completed' THEN
        RAISE EXCEPTION 'completed retrieval vector index %/% is immutable', OLD.ingestion_run_id, OLD.index_version;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_completed_retrieval_vector_index ON retrieval_vector_indexes;
CREATE TRIGGER trg_guard_completed_retrieval_vector_index
BEFORE UPDATE OR DELETE ON retrieval_vector_indexes
FOR EACH ROW EXECUTE FUNCTION guard_completed_retrieval_vector_index();
