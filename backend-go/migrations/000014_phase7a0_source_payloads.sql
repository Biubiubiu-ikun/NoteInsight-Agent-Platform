CREATE TABLE IF NOT EXISTS evidence_source_payloads (
    evidence_source_id BIGINT PRIMARY KEY REFERENCES evidence_sources(id) ON DELETE RESTRICT,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    source_type VARCHAR(40) NOT NULL,
    source_id BIGINT NOT NULL,
    source_version BIGINT NOT NULL,
    canonical_text TEXT NOT NULL,
    source_payload JSONB NOT NULL,
    payload_hash CHAR(64) NOT NULL,
    payload_schema VARCHAR(48) NOT NULL DEFAULT 'source_payload_v1',
    captured_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, source_type, source_id, source_version)
);

CREATE INDEX IF NOT EXISTS idx_evidence_source_payloads_logical_source
    ON evidence_source_payloads(project_id, source_type, source_id, source_version);

CREATE OR REPLACE FUNCTION guard_evidence_source_identity()
RETURNS TRIGGER AS $$
BEGIN
    IF ROW(NEW.project_id, NEW.source_type, NEW.source_id, NEW.source_version, NEW.content_hash)
       IS DISTINCT FROM
       ROW(OLD.project_id, OLD.source_type, OLD.source_id, OLD.source_version, OLD.content_hash) THEN
        RAISE EXCEPTION 'evidence source identity and content hash are immutable for row %', OLD.id;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_evidence_source_identity ON evidence_sources;
CREATE TRIGGER trg_guard_evidence_source_identity
BEFORE UPDATE ON evidence_sources
FOR EACH ROW EXECUTE FUNCTION guard_evidence_source_identity();

CREATE OR REPLACE FUNCTION guard_evidence_source_payload()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'evidence source payload % is immutable', OLD.evidence_source_id;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_evidence_source_payload ON evidence_source_payloads;
CREATE TRIGGER trg_guard_evidence_source_payload
BEFORE UPDATE OR DELETE ON evidence_source_payloads
FOR EACH ROW EXECUTE FUNCTION guard_evidence_source_payload();

CREATE OR REPLACE FUNCTION capture_evidence_source_payload()
RETURNS TRIGGER AS $$
DECLARE
    captured_text TEXT;
    captured_payload JSONB;
BEGIN
    CASE NEW.source_type
        WHEN 'note' THEN
            SELECT
                concat_ws(E'\n\n', NULLIF(n.title, ''), NULLIF(n.body, '')),
                jsonb_build_object(
                    'title', n.title, 'body', n.body, 'category', n.category,
                    'topics', n.topics, 'tags', n.tags, 'location', n.location,
                    'product_entities', n.product_entities, 'note_type', n.note_type,
                    'status', n.status, 'visibility', n.visibility,
                    'created_at', n.created_at, 'updated_at', n.updated_at, 'deleted_at', n.deleted_at
                )
            INTO captured_text, captured_payload
            FROM notes n
            WHERE n.id = NEW.source_id AND n.content_version = NEW.source_version;
        WHEN 'note_media' THEN
            SELECT
                concat_ws(E'\n\n', NULLIF(m.caption, ''), NULLIF(m.ocr_text, '')),
                jsonb_build_object(
                    'note_id', m.note_id, 'media_type', m.media_type, 'url', m.url,
                    'caption', m.caption, 'ocr_text', m.ocr_text, 'position', m.position,
                    'metadata', m.metadata, 'created_at', m.created_at, 'updated_at', m.updated_at
                )
            INTO captured_text, captured_payload
            FROM note_media m
            WHERE m.id = NEW.source_id AND m.content_version = NEW.source_version;
        WHEN 'note_comment' THEN
            SELECT
                c.content,
                jsonb_build_object(
                    'note_id', c.note_id, 'user_id', c.user_id, 'parent_id', c.parent_id,
                    'root_id', c.root_id, 'content', c.content, 'sentiment', c.sentiment,
                    'intent', c.intent, 'topic_id', c.topic_id, 'status', c.status,
                    'created_at', c.created_at, 'updated_at', c.updated_at, 'deleted_at', c.deleted_at
                )
            INTO captured_text, captured_payload
            FROM note_comments c
            WHERE c.id = NEW.source_id AND c.content_version = NEW.source_version;
        ELSE
            RAISE EXCEPTION 'unsupported evidence source type %', NEW.source_type;
    END CASE;

    IF captured_payload IS NULL THEN
        RAISE EXCEPTION 'cannot capture payload for evidence source %/% version %', NEW.source_type, NEW.source_id, NEW.source_version;
    END IF;

    INSERT INTO evidence_source_payloads (
        evidence_source_id, project_id, source_type, source_id, source_version,
        canonical_text, source_payload, payload_hash
    ) VALUES (
        NEW.id, NEW.project_id, NEW.source_type, NEW.source_id, NEW.source_version,
        captured_text, captured_payload,
        encode(digest(convert_to(captured_payload::text, 'UTF8'), 'sha256'), 'hex')
    );
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_capture_evidence_source_payload ON evidence_sources;
CREATE TRIGGER trg_capture_evidence_source_payload
AFTER INSERT ON evidence_sources
FOR EACH ROW EXECUTE FUNCTION capture_evidence_source_payload();

INSERT INTO evidence_source_payloads (
    evidence_source_id, project_id, source_type, source_id, source_version,
    canonical_text, source_payload, payload_hash
)
SELECT
    es.id, es.project_id, es.source_type, es.source_id, es.source_version,
    payload.canonical_text, payload.source_payload,
    encode(digest(convert_to(payload.source_payload::text, 'UTF8'), 'sha256'), 'hex')
FROM evidence_sources es
JOIN notes n
  ON es.source_type = 'note'
 AND n.id = es.source_id
 AND n.content_version = es.source_version
CROSS JOIN LATERAL (
    SELECT
        concat_ws(E'\n\n', NULLIF(n.title, ''), NULLIF(n.body, '')) AS canonical_text,
        jsonb_build_object(
            'title', n.title, 'body', n.body, 'category', n.category,
            'topics', n.topics, 'tags', n.tags, 'location', n.location,
            'product_entities', n.product_entities, 'note_type', n.note_type,
            'status', n.status, 'visibility', n.visibility,
            'created_at', n.created_at, 'updated_at', n.updated_at, 'deleted_at', n.deleted_at
        ) AS source_payload
) payload
ON CONFLICT (evidence_source_id) DO NOTHING;

INSERT INTO evidence_source_payloads (
    evidence_source_id, project_id, source_type, source_id, source_version,
    canonical_text, source_payload, payload_hash
)
SELECT
    es.id, es.project_id, es.source_type, es.source_id, es.source_version,
    payload.canonical_text, payload.source_payload,
    encode(digest(convert_to(payload.source_payload::text, 'UTF8'), 'sha256'), 'hex')
FROM evidence_sources es
JOIN note_media m
  ON es.source_type = 'note_media'
 AND m.id = es.source_id
 AND m.content_version = es.source_version
CROSS JOIN LATERAL (
    SELECT
        concat_ws(E'\n\n', NULLIF(m.caption, ''), NULLIF(m.ocr_text, '')) AS canonical_text,
        jsonb_build_object(
            'note_id', m.note_id, 'media_type', m.media_type, 'url', m.url,
            'caption', m.caption, 'ocr_text', m.ocr_text, 'position', m.position,
            'metadata', m.metadata, 'created_at', m.created_at, 'updated_at', m.updated_at
        ) AS source_payload
) payload
ON CONFLICT (evidence_source_id) DO NOTHING;

INSERT INTO evidence_source_payloads (
    evidence_source_id, project_id, source_type, source_id, source_version,
    canonical_text, source_payload, payload_hash
)
SELECT
    es.id, es.project_id, es.source_type, es.source_id, es.source_version,
    c.content, payload.source_payload,
    encode(digest(convert_to(payload.source_payload::text, 'UTF8'), 'sha256'), 'hex')
FROM evidence_sources es
JOIN note_comments c
  ON es.source_type = 'note_comment'
 AND c.id = es.source_id
 AND c.content_version = es.source_version
CROSS JOIN LATERAL (
    SELECT jsonb_build_object(
        'note_id', c.note_id, 'user_id', c.user_id, 'parent_id', c.parent_id,
        'root_id', c.root_id, 'content', c.content, 'sentiment', c.sentiment,
        'intent', c.intent, 'topic_id', c.topic_id, 'status', c.status,
        'created_at', c.created_at, 'updated_at', c.updated_at, 'deleted_at', c.deleted_at
    ) AS source_payload
) payload
ON CONFLICT (evidence_source_id) DO NOTHING;

DO $$
DECLARE
    missing_frozen_payloads BIGINT;
BEGIN
    SELECT COUNT(*) INTO missing_frozen_payloads
    FROM dataset_version_sources dvs
    LEFT JOIN evidence_source_payloads esp ON esp.evidence_source_id = dvs.evidence_source_id
    WHERE esp.evidence_source_id IS NULL;

    IF missing_frozen_payloads <> 0 THEN
        RAISE EXCEPTION '% frozen source references have no reconstructible immutable payload', missing_frozen_payloads;
    END IF;
END $$;

DROP TRIGGER IF EXISTS trg_notes_evidence_source ON notes;
CREATE TRIGGER trg_notes_evidence_source
AFTER INSERT OR UPDATE OR DELETE ON notes
FOR EACH ROW EXECUTE FUNCTION sync_note_evidence_source();
