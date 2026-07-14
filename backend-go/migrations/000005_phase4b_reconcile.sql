CREATE INDEX IF NOT EXISTS idx_outbox_processing_updated
    ON outbox_events(updated_at)
    WHERE status = 'processing';
