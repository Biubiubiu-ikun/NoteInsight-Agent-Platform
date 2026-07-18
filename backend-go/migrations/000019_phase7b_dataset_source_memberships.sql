CREATE TABLE IF NOT EXISTS dataset_source_memberships (
    dataset_id BIGINT NOT NULL REFERENCES datasets(id) ON DELETE RESTRICT,
    evidence_source_id BIGINT NOT NULL REFERENCES evidence_sources(id) ON DELETE RESTRICT,
    membership_reason VARCHAR(48) NOT NULL DEFAULT 'default_dataset',
    source_run_id VARCHAR(128),
    added_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (dataset_id, evidence_source_id)
);

CREATE INDEX IF NOT EXISTS idx_dataset_source_memberships_source
    ON dataset_source_memberships(evidence_source_id, dataset_id);

INSERT INTO dataset_source_memberships (
    dataset_id, evidence_source_id, membership_reason, created_at
)
SELECT es.dataset_id, es.id, 'legacy_dataset_id', es.created_at
FROM evidence_sources es
WHERE es.dataset_id IS NOT NULL
ON CONFLICT (dataset_id, evidence_source_id) DO NOTHING;

CREATE OR REPLACE FUNCTION sync_default_dataset_source_membership()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.dataset_id IS NOT NULL THEN
        INSERT INTO dataset_source_memberships (
            dataset_id, evidence_source_id, membership_reason
        ) VALUES (NEW.dataset_id, NEW.id, 'default_dataset')
        ON CONFLICT (dataset_id, evidence_source_id) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_evidence_source_default_dataset_membership ON evidence_sources;
CREATE TRIGGER trg_evidence_source_default_dataset_membership
AFTER INSERT OR UPDATE OF dataset_id ON evidence_sources
FOR EACH ROW EXECUTE FUNCTION sync_default_dataset_source_membership();

CREATE OR REPLACE FUNCTION guard_dataset_source_membership()
RETURNS TRIGGER AS $$
DECLARE
    target_dataset_id BIGINT;
    target_status VARCHAR(24);
BEGIN
    target_dataset_id := CASE WHEN TG_OP='DELETE' THEN OLD.dataset_id ELSE NEW.dataset_id END;
    SELECT status INTO target_status FROM datasets WHERE id=target_dataset_id;
    IF target_status <> 'active' THEN
        RAISE EXCEPTION 'dataset % source membership is not mutable in status %', target_dataset_id, target_status;
    END IF;
    IF TG_OP='DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_dataset_source_membership ON dataset_source_memberships;
CREATE TRIGGER trg_guard_dataset_source_membership
BEFORE INSERT OR UPDATE OR DELETE ON dataset_source_memberships
FOR EACH ROW EXECUTE FUNCTION guard_dataset_source_membership();
