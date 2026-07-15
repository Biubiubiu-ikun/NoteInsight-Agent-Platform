CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE note_comments ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE OR REPLACE FUNCTION ensure_project_default_dataset()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO datasets (project_id, slug, name, description, status)
    VALUES (NEW.id, 'community', 'Community Content', 'Default project content dataset', 'active')
    ON CONFLICT (project_id, slug) DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_projects_default_dataset ON projects;
CREATE TRIGGER trg_projects_default_dataset
AFTER INSERT ON projects
FOR EACH ROW EXECUTE FUNCTION ensure_project_default_dataset();

INSERT INTO datasets (project_id, slug, name, description, status)
SELECT id, 'community', 'Community Content', 'Default project content dataset', 'active'
FROM projects
ON CONFLICT (project_id, slug) DO NOTHING;

CREATE OR REPLACE FUNCTION include_note_in_default_dataset()
RETURNS TRIGGER AS $$
DECLARE
    target_dataset_id BIGINT;
BEGIN
    SELECT id INTO target_dataset_id
    FROM datasets
    WHERE project_id = NEW.project_id AND slug = 'community' AND status = 'active'
    ORDER BY id
    LIMIT 1;

    IF target_dataset_id IS NOT NULL THEN
        INSERT INTO dataset_notes (dataset_id, note_id, note_version)
        VALUES (target_dataset_id, NEW.id, NEW.content_version)
        ON CONFLICT (dataset_id, note_id) DO UPDATE
        SET note_version = EXCLUDED.note_version,
            included_at = now();
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_notes_default_dataset ON notes;
CREATE TRIGGER trg_notes_default_dataset
AFTER INSERT OR UPDATE OF content_version, project_id ON notes
FOR EACH ROW EXECUTE FUNCTION include_note_in_default_dataset();

INSERT INTO dataset_notes (dataset_id, note_id, note_version)
SELECT d.id, n.id, n.content_version
FROM notes n
JOIN datasets d ON d.project_id = n.project_id AND d.slug = 'community' AND d.status = 'active'
ON CONFLICT (dataset_id, note_id) DO UPDATE
SET note_version = EXCLUDED.note_version;

CREATE INDEX IF NOT EXISTS idx_evidence_sources_source
    ON evidence_sources(source_type, source_id, source_version DESC);

CREATE OR REPLACE FUNCTION sync_note_evidence_source()
RETURNS TRIGGER AS $$
DECLARE
    target_dataset_id BIGINT;
    effective_deleted_at TIMESTAMPTZ;
BEGIN
    IF TG_OP = 'DELETE' THEN
        UPDATE evidence_sources
        SET index_status = 'deleted', deleted_at = COALESCE(deleted_at, now()), updated_at = now()
        WHERE project_id = OLD.project_id AND source_type = 'note' AND source_id = OLD.id;
        RETURN OLD;
    END IF;

    SELECT id INTO target_dataset_id
    FROM datasets
    WHERE project_id = NEW.project_id AND slug = 'community' AND status = 'active'
    ORDER BY id
    LIMIT 1;

    effective_deleted_at := CASE
        WHEN NEW.deleted_at IS NOT NULL THEN NEW.deleted_at
        WHEN NEW.status = 'deleted' THEN NEW.updated_at
        ELSE NULL
    END;

    UPDATE evidence_sources
    SET index_status = 'deleted', deleted_at = COALESCE(deleted_at, NEW.updated_at), updated_at = now()
    WHERE project_id = NEW.project_id
      AND source_type = 'note'
      AND source_id = NEW.id
      AND source_version < NEW.content_version
      AND index_status <> 'deleted';

    INSERT INTO evidence_sources (
        project_id, dataset_id, source_type, source_id, source_version,
        visibility, content_hash, index_status, metadata, deleted_at
    ) VALUES (
        NEW.project_id,
        target_dataset_id,
        'note',
        NEW.id,
        NEW.content_version,
        NEW.visibility,
        encode(digest(concat_ws(E'\x1f', NEW.title, NEW.body, NEW.status, NEW.visibility), 'sha256'), 'hex'),
        CASE WHEN effective_deleted_at IS NULL THEN 'pending' ELSE 'deleted' END,
        jsonb_build_object('note_id', NEW.id, 'author_id', NEW.author_id, 'category', NEW.category),
        effective_deleted_at
    )
    ON CONFLICT (project_id, source_type, source_id, source_version) DO UPDATE
    SET dataset_id = EXCLUDED.dataset_id,
        visibility = EXCLUDED.visibility,
        content_hash = EXCLUDED.content_hash,
        index_status = CASE
            WHEN EXCLUDED.deleted_at IS NOT NULL THEN 'deleted'
            WHEN evidence_sources.content_hash <> EXCLUDED.content_hash THEN 'pending'
            ELSE evidence_sources.index_status
        END,
        metadata = EXCLUDED.metadata,
        deleted_at = EXCLUDED.deleted_at,
        updated_at = now();

    UPDATE evidence_sources es
    SET visibility = NEW.visibility,
        index_status = CASE
            WHEN effective_deleted_at IS NOT NULL OR es.deleted_at IS NOT NULL THEN 'deleted'
            ELSE 'pending'
        END,
        deleted_at = CASE
            WHEN effective_deleted_at IS NOT NULL THEN effective_deleted_at
            ELSE es.deleted_at
        END,
        updated_at = now()
    WHERE es.project_id = NEW.project_id
      AND (
          (es.source_type = 'note_media' AND EXISTS (
              SELECT 1 FROM note_media m WHERE m.id = es.source_id AND m.note_id = NEW.id
          ))
          OR
          (es.source_type = 'note_comment' AND EXISTS (
              SELECT 1 FROM note_comments c WHERE c.id = es.source_id AND c.note_id = NEW.id
          ))
      );

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_notes_evidence_source ON notes;
CREATE TRIGGER trg_notes_evidence_source
AFTER INSERT OR UPDATE OF title, body, status, visibility, content_version, deleted_at OR DELETE ON notes
FOR EACH ROW EXECUTE FUNCTION sync_note_evidence_source();

CREATE OR REPLACE FUNCTION sync_note_media_evidence_source()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        UPDATE evidence_sources
        SET index_status = 'deleted', deleted_at = COALESCE(deleted_at, now()), updated_at = now()
        WHERE source_type = 'note_media' AND source_id = OLD.id;
        RETURN OLD;
    END IF;

    INSERT INTO evidence_sources (
        project_id, dataset_id, source_type, source_id, source_version,
        visibility, content_hash, index_status, metadata, deleted_at
    )
    SELECT
        n.project_id,
        d.id,
        'note_media',
        NEW.id,
        1,
        n.visibility,
        encode(digest(concat_ws(E'\x1f', NEW.media_type, NEW.url, NEW.caption, NEW.ocr_text, NEW.position::text), 'sha256'), 'hex'),
        CASE WHEN n.deleted_at IS NULL AND n.status <> 'deleted' THEN 'pending' ELSE 'deleted' END,
        jsonb_build_object('note_id', NEW.note_id, 'media_type', NEW.media_type, 'position', NEW.position),
        CASE WHEN n.deleted_at IS NULL AND n.status <> 'deleted' THEN NULL ELSE COALESCE(n.deleted_at, n.updated_at) END
    FROM notes n
    LEFT JOIN LATERAL (
        SELECT id FROM datasets
        WHERE project_id = n.project_id AND slug = 'community' AND status = 'active'
        ORDER BY id LIMIT 1
    ) d ON true
    WHERE n.id = NEW.note_id
    ON CONFLICT (project_id, source_type, source_id, source_version) DO UPDATE
    SET dataset_id = EXCLUDED.dataset_id,
        visibility = EXCLUDED.visibility,
        content_hash = EXCLUDED.content_hash,
        index_status = CASE WHEN EXCLUDED.deleted_at IS NULL THEN 'pending' ELSE 'deleted' END,
        metadata = EXCLUDED.metadata,
        deleted_at = EXCLUDED.deleted_at,
        updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_note_media_evidence_source ON note_media;
CREATE TRIGGER trg_note_media_evidence_source
AFTER INSERT OR UPDATE OF media_type, url, caption, ocr_text, position OR DELETE ON note_media
FOR EACH ROW EXECUTE FUNCTION sync_note_media_evidence_source();

CREATE OR REPLACE FUNCTION sync_note_comment_evidence_source()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        UPDATE evidence_sources
        SET index_status = 'deleted', deleted_at = COALESCE(deleted_at, now()), updated_at = now()
        WHERE source_type = 'note_comment' AND source_id = OLD.id;
        RETURN OLD;
    END IF;

    INSERT INTO evidence_sources (
        project_id, dataset_id, source_type, source_id, source_version,
        visibility, content_hash, index_status, metadata, deleted_at
    )
    SELECT
        n.project_id,
        d.id,
        'note_comment',
        NEW.id,
        1,
        n.visibility,
        encode(digest(concat_ws(E'\x1f', NEW.content, NEW.status, COALESCE(NEW.parent_id::text, '')), 'sha256'), 'hex'),
        CASE
            WHEN n.deleted_at IS NULL AND n.status <> 'deleted' AND NEW.deleted_at IS NULL AND NEW.status = 1
            THEN 'pending' ELSE 'deleted'
        END,
        jsonb_build_object('note_id', NEW.note_id, 'user_id', NEW.user_id, 'parent_id', NEW.parent_id),
        CASE
            WHEN n.deleted_at IS NULL AND n.status <> 'deleted' AND NEW.deleted_at IS NULL AND NEW.status = 1
            THEN NULL ELSE COALESCE(NEW.deleted_at, n.deleted_at, NEW.updated_at)
        END
    FROM notes n
    LEFT JOIN LATERAL (
        SELECT id FROM datasets
        WHERE project_id = n.project_id AND slug = 'community' AND status = 'active'
        ORDER BY id LIMIT 1
    ) d ON true
    WHERE n.id = NEW.note_id
    ON CONFLICT (project_id, source_type, source_id, source_version) DO UPDATE
    SET dataset_id = EXCLUDED.dataset_id,
        visibility = EXCLUDED.visibility,
        content_hash = EXCLUDED.content_hash,
        index_status = CASE WHEN EXCLUDED.deleted_at IS NULL THEN 'pending' ELSE 'deleted' END,
        metadata = EXCLUDED.metadata,
        deleted_at = EXCLUDED.deleted_at,
        updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_note_comments_evidence_source ON note_comments;
CREATE TRIGGER trg_note_comments_evidence_source
AFTER INSERT OR UPDATE OF content, status, parent_id, deleted_at OR DELETE ON note_comments
FOR EACH ROW EXECUTE FUNCTION sync_note_comment_evidence_source();

INSERT INTO evidence_sources (
    project_id, dataset_id, source_type, source_id, source_version,
    visibility, content_hash, index_status, metadata, deleted_at
)
SELECT
    n.project_id,
    d.id,
    'note',
    n.id,
    n.content_version,
    n.visibility,
    encode(digest(concat_ws(E'\x1f', n.title, n.body, n.status, n.visibility), 'sha256'), 'hex'),
    CASE WHEN n.deleted_at IS NULL AND n.status <> 'deleted' THEN 'pending' ELSE 'deleted' END,
    jsonb_build_object('note_id', n.id, 'author_id', n.author_id, 'category', n.category),
    CASE WHEN n.deleted_at IS NULL AND n.status <> 'deleted' THEN NULL ELSE COALESCE(n.deleted_at, n.updated_at) END
FROM notes n
LEFT JOIN LATERAL (
    SELECT id FROM datasets
    WHERE project_id = n.project_id AND slug = 'community' AND status = 'active'
    ORDER BY id LIMIT 1
) d ON true
ON CONFLICT (project_id, source_type, source_id, source_version) DO UPDATE
SET dataset_id = EXCLUDED.dataset_id,
    visibility = EXCLUDED.visibility,
    content_hash = EXCLUDED.content_hash,
    index_status = EXCLUDED.index_status,
    metadata = EXCLUDED.metadata,
    deleted_at = EXCLUDED.deleted_at,
    updated_at = now();

INSERT INTO evidence_sources (
    project_id, dataset_id, source_type, source_id, source_version,
    visibility, content_hash, index_status, metadata, deleted_at
)
SELECT
    n.project_id,
    d.id,
    'note_media',
    m.id,
    1,
    n.visibility,
    encode(digest(concat_ws(E'\x1f', m.media_type, m.url, m.caption, m.ocr_text, m.position::text), 'sha256'), 'hex'),
    CASE WHEN n.deleted_at IS NULL AND n.status <> 'deleted' THEN 'pending' ELSE 'deleted' END,
    jsonb_build_object('note_id', m.note_id, 'media_type', m.media_type, 'position', m.position),
    CASE WHEN n.deleted_at IS NULL AND n.status <> 'deleted' THEN NULL ELSE COALESCE(n.deleted_at, n.updated_at) END
FROM note_media m
JOIN notes n ON n.id = m.note_id
LEFT JOIN LATERAL (
    SELECT id FROM datasets
    WHERE project_id = n.project_id AND slug = 'community' AND status = 'active'
    ORDER BY id LIMIT 1
) d ON true
ON CONFLICT (project_id, source_type, source_id, source_version) DO UPDATE
SET dataset_id = EXCLUDED.dataset_id,
    visibility = EXCLUDED.visibility,
    content_hash = EXCLUDED.content_hash,
    index_status = EXCLUDED.index_status,
    metadata = EXCLUDED.metadata,
    deleted_at = EXCLUDED.deleted_at,
    updated_at = now();

INSERT INTO evidence_sources (
    project_id, dataset_id, source_type, source_id, source_version,
    visibility, content_hash, index_status, metadata, deleted_at
)
SELECT
    n.project_id,
    d.id,
    'note_comment',
    c.id,
    1,
    n.visibility,
    encode(digest(concat_ws(E'\x1f', c.content, c.status, COALESCE(c.parent_id::text, '')), 'sha256'), 'hex'),
    CASE
        WHEN n.deleted_at IS NULL AND n.status <> 'deleted' AND c.deleted_at IS NULL AND c.status = 1
        THEN 'pending' ELSE 'deleted'
    END,
    jsonb_build_object('note_id', c.note_id, 'user_id', c.user_id, 'parent_id', c.parent_id),
    CASE
        WHEN n.deleted_at IS NULL AND n.status <> 'deleted' AND c.deleted_at IS NULL AND c.status = 1
        THEN NULL ELSE COALESCE(c.deleted_at, n.deleted_at, c.updated_at)
    END
FROM note_comments c
JOIN notes n ON n.id = c.note_id
LEFT JOIN LATERAL (
    SELECT id FROM datasets
    WHERE project_id = n.project_id AND slug = 'community' AND status = 'active'
    ORDER BY id LIMIT 1
) d ON true
ON CONFLICT (project_id, source_type, source_id, source_version) DO UPDATE
SET dataset_id = EXCLUDED.dataset_id,
    visibility = EXCLUDED.visibility,
    content_hash = EXCLUDED.content_hash,
    index_status = EXCLUDED.index_status,
    metadata = EXCLUDED.metadata,
    deleted_at = EXCLUDED.deleted_at,
    updated_at = now();
