CREATE TABLE IF NOT EXISTS retrieval_lexeme_visibility_stats (
    ingestion_run_id VARCHAR(128) NOT NULL REFERENCES retrieval_lexical_indexes(ingestion_run_id) ON DELETE RESTRICT,
    access_scope VARCHAR(16) NOT NULL CHECK (access_scope IN ('public', 'member')),
    lexeme TEXT NOT NULL,
    chunk_frequency BIGINT NOT NULL CHECK (chunk_frequency > 0),
    occurrence_count BIGINT NOT NULL CHECK (occurrence_count >= chunk_frequency),
    inverse_document_frequency DOUBLE PRECISION NOT NULL CHECK (inverse_document_frequency > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (ingestion_run_id, access_scope, lexeme)
);

CREATE INDEX IF NOT EXISTS idx_retrieval_visibility_lexemes_frequency
    ON retrieval_lexeme_visibility_stats(ingestion_run_id, access_scope, chunk_frequency, lexeme);

CREATE OR REPLACE FUNCTION guard_retrieval_lexeme_visibility_stats()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'visibility-scoped lexeme stats are immutable';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_retrieval_lexeme_visibility_stats ON retrieval_lexeme_visibility_stats;
CREATE TRIGGER trg_guard_retrieval_lexeme_visibility_stats
BEFORE UPDATE OR DELETE ON retrieval_lexeme_visibility_stats
FOR EACH ROW EXECUTE FUNCTION guard_retrieval_lexeme_visibility_stats();
