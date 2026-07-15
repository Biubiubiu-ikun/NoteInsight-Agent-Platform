DROP TRIGGER IF EXISTS trg_notes_evidence_source ON notes;
CREATE TRIGGER trg_notes_evidence_source
AFTER INSERT OR UPDATE OF
    title, body, category, topics, tags, location, product_entities,
    status, visibility, deleted_at, content_version
OR DELETE ON notes
FOR EACH ROW EXECUTE FUNCTION sync_note_evidence_source();
