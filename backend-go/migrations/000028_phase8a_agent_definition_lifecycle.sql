CREATE OR REPLACE FUNCTION validate_agent_run()
RETURNS TRIGGER AS $$
DECLARE
    source_project_id BIGINT;
    source_dataset_version_id BIGINT;
    source_status VARCHAR(24);
    definition_status VARCHAR(24);
BEGIN
    SELECT project_id, dataset_version_id, status
      INTO source_project_id, source_dataset_version_id, source_status
      FROM ingestion_runs
     WHERE run_id = NEW.ingestion_run_id;
    IF NOT FOUND OR source_status <> 'completed'
       OR source_project_id <> NEW.project_id
       OR source_dataset_version_id <> NEW.dataset_version_id THEN
        RAISE EXCEPTION 'agent run retrieval scope is not a completed matching ingestion run';
    END IF;

    IF TG_OP = 'INSERT' THEN
        SELECT status INTO definition_status
          FROM agent_prompt_versions
         WHERE id = NEW.prompt_version_id;
        IF NOT FOUND OR definition_status <> 'frozen' THEN
            RAISE EXCEPTION 'new agent run prompt version must be frozen';
        END IF;
        IF NEW.model_version_id IS NOT NULL THEN
            RAISE EXCEPTION 'new agent runs bind a model only when dispatched';
        END IF;
        IF NEW.status <> 'queued' THEN
            RAISE EXCEPTION 'new agent runs must be queued';
        END IF;
        RETURN NEW;
    END IF;

    IF OLD.status IN ('succeeded', 'failed', 'cancelled') THEN
        RAISE EXCEPTION 'terminal agent run % is immutable', OLD.id;
    END IF;
    IF ROW(
        NEW.id, NEW.project_id, NEW.dataset_version_id, NEW.ingestion_run_id,
        NEW.requested_by, NEW.query, NEW.requested_mode, NEW.retrieval_plan,
        NEW.prompt_version_id, NEW.max_steps,
        NEW.max_retrieval_calls, NEW.max_model_calls, NEW.max_input_tokens,
        NEW.max_output_tokens, NEW.max_duration_ms, NEW.max_cost_micros,
        NEW.idempotency_key, NEW.request_hash, NEW.request_id, NEW.trace_id,
        NEW.created_at
    ) IS DISTINCT FROM ROW(
        OLD.id, OLD.project_id, OLD.dataset_version_id, OLD.ingestion_run_id,
        OLD.requested_by, OLD.query, OLD.requested_mode, OLD.retrieval_plan,
        OLD.prompt_version_id, OLD.max_steps,
        OLD.max_retrieval_calls, OLD.max_model_calls, OLD.max_input_tokens,
        OLD.max_output_tokens, OLD.max_duration_ms, OLD.max_cost_micros,
        OLD.idempotency_key, OLD.request_hash, OLD.request_id, OLD.trace_id,
        OLD.created_at
    ) THEN
        RAISE EXCEPTION 'agent run % immutable request lineage changed', OLD.id;
    END IF;
    IF NEW.model_version_id IS DISTINCT FROM OLD.model_version_id THEN
        IF NOT (
            OLD.status = 'queued' AND NEW.status = 'running'
            AND OLD.model_version_id IS NULL AND NEW.model_version_id IS NOT NULL
        ) THEN
            RAISE EXCEPTION 'agent run % model lineage may only bind once at dispatch', OLD.id;
        END IF;
        SELECT status INTO definition_status
          FROM agent_model_versions
         WHERE id = NEW.model_version_id;
        IF NOT FOUND OR definition_status <> 'frozen' THEN
            RAISE EXCEPTION 'agent run model version must be frozen at dispatch';
        END IF;
    END IF;
    IF NOT (
        NEW.status = OLD.status
        OR (OLD.status = 'queued' AND NEW.status IN ('running', 'failed', 'cancelled'))
        OR (OLD.status = 'running' AND NEW.status IN ('succeeded', 'failed', 'cancelled'))
    ) THEN
        RAISE EXCEPTION 'invalid agent run status transition % -> %', OLD.status, NEW.status;
    END IF;
    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
