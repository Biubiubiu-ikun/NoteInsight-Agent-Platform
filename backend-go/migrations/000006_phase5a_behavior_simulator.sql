CREATE TABLE IF NOT EXISTS simulation_runs (
    run_id VARCHAR(128) PRIMARY KEY,
    profile VARCHAR(32) NOT NULL,
    scenario VARCHAR(32) NOT NULL,
    seed BIGINT NOT NULL,
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    report JSONB,
    status VARCHAR(32) NOT NULL DEFAULT 'running',
    event_count BIGINT NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('running', 'completed', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_simulation_runs_created
    ON simulation_runs(created_at DESC);

CREATE TABLE IF NOT EXISTS user_behavior_profiles (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    source_run_id VARCHAR(128) REFERENCES simulation_runs(run_id) ON DELETE SET NULL,
    persona VARCHAR(64) NOT NULL,
    activity_level DOUBLE PRECISION NOT NULL,
    positive_ratio DOUBLE PRECISION NOT NULL,
    comment_length_preference VARCHAR(32) NOT NULL,
    like_probability DOUBLE PRECISION NOT NULL,
    collect_probability DOUBLE PRECISION NOT NULL,
    comment_probability DOUBLE PRECISION NOT NULL,
    share_probability DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (activity_level BETWEEN 0 AND 1),
    CHECK (positive_ratio BETWEEN 0 AND 1),
    CHECK (like_probability BETWEEN 0 AND 1),
    CHECK (collect_probability BETWEEN 0 AND 1),
    CHECK (comment_probability BETWEEN 0 AND 1),
    CHECK (share_probability BETWEEN 0 AND 1)
);

CREATE INDEX IF NOT EXISTS idx_user_behavior_profiles_persona
    ON user_behavior_profiles(persona, activity_level DESC);

ALTER TABLE behavior_events
    ADD COLUMN IF NOT EXISTS simulation_run_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS session_id VARCHAR(128),
    ADD COLUMN IF NOT EXISTS sequence_no INT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'fk_behavior_events_simulation_run'
    ) THEN
        ALTER TABLE behavior_events
            ADD CONSTRAINT fk_behavior_events_simulation_run
            FOREIGN KEY (simulation_run_id)
            REFERENCES simulation_runs(run_id)
            ON DELETE CASCADE;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_behavior_simulation_session
    ON behavior_events(simulation_run_id, session_id, sequence_no);

CREATE INDEX IF NOT EXISTS idx_behavior_simulation_time
    ON behavior_events(simulation_run_id, occurred_at);
