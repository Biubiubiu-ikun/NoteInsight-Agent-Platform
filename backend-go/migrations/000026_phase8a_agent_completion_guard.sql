CREATE OR REPLACE FUNCTION validate_agent_claim_citation()
RETURNS TRIGGER AS $$
DECLARE
    run_project_id BIGINT;
    run_dataset_version_id BIGINT;
    run_ingestion_run_id VARCHAR(128);
    run_status VARCHAR(24);
    citation_project_id BIGINT;
    citation_dataset_version_id BIGINT;
    citation_document_id BIGINT;
    citation_quote_hash CHAR(64);
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'agent claim citation rows are immutable';
    END IF;
    SELECT r.project_id, r.dataset_version_id, r.ingestion_run_id, r.status
      INTO run_project_id, run_dataset_version_id, run_ingestion_run_id, run_status
      FROM agent_claims c
      JOIN agent_runs r ON r.id = c.run_id
     WHERE c.id = NEW.claim_id;
    IF NOT FOUND OR run_status IN ('succeeded', 'failed', 'cancelled') THEN
        RAISE EXCEPTION 'cannot mutate citation lineage for terminal or missing agent run';
    END IF;
    SELECT project_id, dataset_version_id, document_id, quote_hash
      INTO citation_project_id, citation_dataset_version_id, citation_document_id, citation_quote_hash
      FROM source_citations
     WHERE id = NEW.source_citation_id;
    IF NOT FOUND
       OR citation_project_id <> run_project_id
       OR citation_dataset_version_id <> run_dataset_version_id
       OR NOT EXISTS (
           SELECT 1
             FROM ingestion_run_documents
            WHERE run_id = run_ingestion_run_id
              AND document_id = citation_document_id
       ) THEN
        RAISE EXCEPTION 'agent claim citation is outside the run retrieval scope';
    END IF;
    IF NEW.quote_hash_snapshot <> citation_quote_hash THEN
        RAISE EXCEPTION 'agent claim citation quote hash does not match canonical citation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION enforce_agent_run_dispatch_and_completion()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.status = 'running' AND OLD.status <> 'running' AND NEW.model_version_id IS NULL THEN
        RAISE EXCEPTION 'running agent run % must bind a frozen model version', OLD.id;
    END IF;
    IF NEW.status = 'succeeded' AND OLD.status <> 'succeeded' AND EXISTS (
        SELECT 1
          FROM agent_claims c
         WHERE c.run_id = NEW.id
           AND c.claim_type = 'factual'
           AND (
               c.support_status <> 'supported'
               OR NOT EXISTS (
                   SELECT 1
                     FROM agent_claim_citations acc
                    WHERE acc.claim_id = c.id
                      AND acc.relationship = 'supports'
               )
           )
    ) THEN
        RAISE EXCEPTION 'agent run % has unsupported or uncited factual claims', OLD.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_enforce_agent_run_dispatch_and_completion ON agent_runs;
CREATE TRIGGER trg_enforce_agent_run_dispatch_and_completion
BEFORE UPDATE OF status, model_version_id ON agent_runs
FOR EACH ROW EXECUTE FUNCTION enforce_agent_run_dispatch_and_completion();
