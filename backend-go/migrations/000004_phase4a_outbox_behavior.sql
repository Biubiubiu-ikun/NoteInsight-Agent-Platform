CREATE TABLE IF NOT EXISTS outbox_events (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(128) NOT NULL UNIQUE,
    aggregate_type VARCHAR(64) NOT NULL,
    aggregate_id BIGINT NOT NULL,
    event_type VARCHAR(128) NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    retry_count INT NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_outbox_status_retry
    ON outbox_events(status, next_retry_at, created_at);

CREATE INDEX IF NOT EXISTS idx_outbox_aggregate_created
    ON outbox_events(aggregate_type, aggregate_id, created_at DESC);

CREATE TABLE IF NOT EXISTS processed_events (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(128) NOT NULL,
    consumer_name VARCHAR(128) NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (event_id, consumer_name)
);

CREATE TABLE IF NOT EXISTS behavior_events (
    id BIGSERIAL PRIMARY KEY,
    source_event_id VARCHAR(128) NOT NULL UNIQUE,
    project_id BIGINT NOT NULL DEFAULT 0,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    note_id BIGINT REFERENCES notes(id) ON DELETE CASCADE,
    comment_id BIGINT REFERENCES note_comments(id) ON DELETE CASCADE,
    event_type VARCHAR(64) NOT NULL,
    event_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_behavior_note_time
    ON behavior_events(note_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_behavior_user_time
    ON behavior_events(user_id, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_behavior_type_time
    ON behavior_events(event_type, occurred_at DESC);
