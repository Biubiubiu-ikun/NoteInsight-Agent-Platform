DROP TRIGGER IF EXISTS trg_guard_completed_retrieval_vector_index ON retrieval_vector_indexes;

ALTER TABLE retrieval_vector_indexes
    ADD COLUMN IF NOT EXISTS checkpoint_chunk_id BIGINT NOT NULL DEFAULT 0 CHECK (checkpoint_chunk_id >= 0),
    ADD COLUMN IF NOT EXISTS checkpoint_point_count BIGINT NOT NULL DEFAULT 0 CHECK (checkpoint_point_count >= 0),
    ADD COLUMN IF NOT EXISTS build_attempt INTEGER NOT NULL DEFAULT 0 CHECK (build_attempt >= 0),
    ADD COLUMN IF NOT EXISTS lease_owner VARCHAR(255),
    ADD COLUMN IF NOT EXISTS lease_token VARCHAR(128),
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_reconciled_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS orphan_point_count BIGINT NOT NULL DEFAULT 0 CHECK (orphan_point_count >= 0),
    ADD COLUMN IF NOT EXISTS missing_point_count BIGINT NOT NULL DEFAULT 0 CHECK (missing_point_count >= 0);

UPDATE retrieval_vector_indexes index_row
SET checkpoint_point_count=index_row.point_count,
    checkpoint_chunk_id=COALESCE((
        SELECT max(chunk.id)
        FROM ingestion_run_documents run_document
        JOIN evidence_chunks chunk ON chunk.document_id=run_document.document_id
        WHERE run_document.run_id=index_row.ingestion_run_id
    ),0),
    build_attempt=GREATEST(index_row.build_attempt,1),
    last_reconciled_at=COALESCE(index_row.last_reconciled_at,index_row.completed_at,index_row.updated_at)
WHERE index_row.status='completed';

UPDATE retrieval_vector_indexes
SET status='failed',
    error_message='legacy building row released by phase7d recovery migration',
    lease_owner=NULL,
    lease_token=NULL,
    lease_expires_at=NULL,
    heartbeat_at=NULL,
    updated_at=clock_timestamp()
WHERE status='building';

ALTER TABLE retrieval_vector_indexes
    DROP CONSTRAINT IF EXISTS chk_retrieval_vector_checkpoint_count,
    DROP CONSTRAINT IF EXISTS chk_retrieval_vector_lease_shape,
    DROP CONSTRAINT IF EXISTS chk_retrieval_vector_completed_checkpoint;

ALTER TABLE retrieval_vector_indexes
    ADD CONSTRAINT chk_retrieval_vector_checkpoint_count
        CHECK (checkpoint_point_count <= point_count),
    ADD CONSTRAINT chk_retrieval_vector_lease_shape
        CHECK ((status='building' AND lease_owner IS NOT NULL AND lease_token IS NOT NULL
                AND lease_expires_at IS NOT NULL AND heartbeat_at IS NOT NULL)
            OR (status<>'building' AND lease_owner IS NULL AND lease_token IS NULL
                AND lease_expires_at IS NULL)),
    ADD CONSTRAINT chk_retrieval_vector_completed_checkpoint
        CHECK (status<>'completed' OR checkpoint_point_count=point_count);

CREATE INDEX IF NOT EXISTS idx_retrieval_vector_indexes_lease
    ON retrieval_vector_indexes(status,lease_expires_at)
    WHERE status='building';

CREATE TRIGGER trg_guard_completed_retrieval_vector_index
BEFORE UPDATE OR DELETE ON retrieval_vector_indexes
FOR EACH ROW EXECUTE FUNCTION guard_completed_retrieval_vector_index();
