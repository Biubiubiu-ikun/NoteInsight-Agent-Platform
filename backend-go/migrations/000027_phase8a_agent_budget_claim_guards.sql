DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_runs_input_token_budget_check') THEN
        ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_input_token_budget_check
            CHECK (used_input_tokens <= max_input_tokens);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_runs_output_token_budget_check') THEN
        ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_output_token_budget_check
            CHECK (used_output_tokens <= max_output_tokens);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_runs_cost_budget_check') THEN
        ALTER TABLE agent_runs ADD CONSTRAINT agent_runs_cost_budget_check
            CHECK (used_cost_micros <= max_cost_micros);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'agent_claims_claim_type_check') THEN
        ALTER TABLE agent_claims ADD CONSTRAINT agent_claims_claim_type_check
            CHECK (claim_type IN ('factual', 'interpretation', 'recommendation'));
    END IF;
END $$;

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
        RAISE EXCEPTION 'agent run % has unsupported or uncited claims', OLD.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
