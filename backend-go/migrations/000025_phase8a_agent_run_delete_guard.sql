CREATE OR REPLACE FUNCTION guard_agent_run_delete()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'agent run % and its audit lineage cannot be deleted', OLD.id;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_agent_run_delete ON agent_runs;
CREATE TRIGGER trg_guard_agent_run_delete
BEFORE DELETE ON agent_runs
FOR EACH ROW EXECUTE FUNCTION guard_agent_run_delete();
