CREATE TABLE IF NOT EXISTS content_corpus_runs (
    run_id VARCHAR(128) PRIMARY KEY,
    profile VARCHAR(32) NOT NULL,
    seed BIGINT NOT NULL,
    config JSONB NOT NULL,
    report JSONB,
    status VARCHAR(32) NOT NULL DEFAULT 'running',
    note_count INT NOT NULL DEFAULT 0,
    media_count INT NOT NULL DEFAULT 0,
    comment_count INT NOT NULL DEFAULT 0,
    eval_case_count INT NOT NULL DEFAULT 0,
    started_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (status IN ('running', 'completed', 'failed'))
);

CREATE INDEX IF NOT EXISTS idx_content_corpus_runs_created
    ON content_corpus_runs(created_at DESC);

CREATE TABLE IF NOT EXISTS content_scenarios (
    note_id BIGINT PRIMARY KEY REFERENCES notes(id) ON DELETE CASCADE,
    run_id VARCHAR(128) NOT NULL REFERENCES content_corpus_runs(run_id) ON DELETE CASCADE,
    category VARCHAR(64) NOT NULL,
    subject TEXT NOT NULL,
    scenario JSONB NOT NULL,
    scenario_version VARCHAR(32) NOT NULL DEFAULT 'phase5b_v1',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_content_scenarios_run_category
    ON content_scenarios(run_id, category, note_id);

CREATE TABLE IF NOT EXISTS content_eval_cases (
    id BIGSERIAL PRIMARY KEY,
    run_id VARCHAR(128) NOT NULL REFERENCES content_corpus_runs(run_id) ON DELETE CASCADE,
    note_id BIGINT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    task_type VARCHAR(64) NOT NULL,
    question TEXT NOT NULL,
    expected_answer TEXT NOT NULL,
    gold_sources JSONB NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, note_id, task_type)
);

CREATE INDEX IF NOT EXISTS idx_content_eval_cases_run_task
    ON content_eval_cases(run_id, task_type, note_id);

CREATE INDEX IF NOT EXISTS idx_content_eval_cases_note
    ON content_eval_cases(note_id, id);
