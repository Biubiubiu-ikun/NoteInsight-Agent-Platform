ALTER TABLE note_media
    ADD COLUMN IF NOT EXISTS content_version BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE note_comments
    ADD COLUMN IF NOT EXISTS content_version BIGINT NOT NULL DEFAULT 1;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'note_media_content_version_check') THEN
        ALTER TABLE note_media ADD CONSTRAINT note_media_content_version_check CHECK (content_version > 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'note_comments_content_version_check') THEN
        ALTER TABLE note_comments ADD CONSTRAINT note_comments_content_version_check CHECK (content_version > 0);
    END IF;
END $$;

CREATE OR REPLACE FUNCTION enforce_note_content_version()
RETURNS TRIGGER AS $$
BEGIN
    IF ROW(
        NEW.title, NEW.body, NEW.category, NEW.topics, NEW.tags,
        NEW.location, NEW.product_entities, NEW.status, NEW.visibility, NEW.deleted_at
    ) IS DISTINCT FROM ROW(
        OLD.title, OLD.body, OLD.category, OLD.topics, OLD.tags,
        OLD.location, OLD.product_entities, OLD.status, OLD.visibility, OLD.deleted_at
    ) THEN
        NEW.content_version := OLD.content_version + 1;
    ELSE
        NEW.content_version := OLD.content_version;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_notes_content_version ON notes;
CREATE TRIGGER trg_notes_content_version
BEFORE UPDATE OF title, body, category, topics, tags, location, product_entities, status, visibility, deleted_at, content_version ON notes
FOR EACH ROW EXECUTE FUNCTION enforce_note_content_version();

CREATE OR REPLACE FUNCTION enforce_note_media_content_version()
RETURNS TRIGGER AS $$
BEGIN
    IF ROW(NEW.media_type, NEW.url, NEW.caption, NEW.ocr_text, NEW.position, NEW.metadata)
       IS DISTINCT FROM
       ROW(OLD.media_type, OLD.url, OLD.caption, OLD.ocr_text, OLD.position, OLD.metadata) THEN
        NEW.content_version := OLD.content_version + 1;
        NEW.updated_at := now();
    ELSE
        NEW.content_version := OLD.content_version;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_note_media_content_version ON note_media;
CREATE TRIGGER trg_note_media_content_version
BEFORE UPDATE OF media_type, url, caption, ocr_text, position, metadata, content_version ON note_media
FOR EACH ROW EXECUTE FUNCTION enforce_note_media_content_version();

CREATE OR REPLACE FUNCTION enforce_note_comment_content_version()
RETURNS TRIGGER AS $$
BEGIN
    IF ROW(NEW.content, NEW.status, NEW.parent_id, NEW.deleted_at, NEW.sentiment, NEW.intent, NEW.topic_id)
       IS DISTINCT FROM
       ROW(OLD.content, OLD.status, OLD.parent_id, OLD.deleted_at, OLD.sentiment, OLD.intent, OLD.topic_id) THEN
        NEW.content_version := OLD.content_version + 1;
        NEW.updated_at := now();
    ELSE
        NEW.content_version := OLD.content_version;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_note_comments_content_version ON note_comments;
CREATE TRIGGER trg_note_comments_content_version
BEFORE UPDATE OF content, status, parent_id, deleted_at, sentiment, intent, topic_id, content_version ON note_comments
FOR EACH ROW EXECUTE FUNCTION enforce_note_comment_content_version();

CREATE OR REPLACE FUNCTION sync_note_media_evidence_source()
RETURNS TRIGGER AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        UPDATE evidence_sources
        SET index_status = 'deleted', deleted_at = COALESCE(deleted_at, now()), updated_at = now()
        WHERE source_type = 'note_media' AND source_id = OLD.id;
        RETURN OLD;
    END IF;

    UPDATE evidence_sources
    SET index_status = 'deleted',
        deleted_at = COALESCE(deleted_at, NEW.updated_at),
        updated_at = now()
    WHERE source_type = 'note_media'
      AND source_id = NEW.id
      AND source_version < NEW.content_version
      AND index_status <> 'deleted';

    INSERT INTO evidence_sources (
        project_id, dataset_id, source_type, source_id, source_version,
        visibility, content_hash, index_status, metadata, deleted_at
    )
    SELECT
        n.project_id,
        d.id,
        'note_media',
        NEW.id,
        NEW.content_version,
        n.visibility,
        encode(digest(concat_ws(E'\x1f', NEW.media_type, NEW.url, NEW.caption, NEW.ocr_text, NEW.position::text), 'sha256'), 'hex'),
        CASE WHEN n.deleted_at IS NULL AND n.status <> 'deleted' THEN 'pending' ELSE 'deleted' END,
        jsonb_build_object(
            'note_id', NEW.note_id,
            'media_type', NEW.media_type,
            'position', NEW.position,
            'content_version', NEW.content_version
        ),
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
        index_status = CASE WHEN EXCLUDED.deleted_at IS NULL THEN evidence_sources.index_status ELSE 'deleted' END,
        metadata = EXCLUDED.metadata,
        deleted_at = EXCLUDED.deleted_at,
        updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_note_media_evidence_source ON note_media;
CREATE TRIGGER trg_note_media_evidence_source
AFTER INSERT OR UPDATE OF media_type, url, caption, ocr_text, position, metadata, content_version OR DELETE ON note_media
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

    UPDATE evidence_sources
    SET index_status = 'deleted',
        deleted_at = COALESCE(deleted_at, NEW.updated_at),
        updated_at = now()
    WHERE source_type = 'note_comment'
      AND source_id = NEW.id
      AND source_version < NEW.content_version
      AND index_status <> 'deleted';

    INSERT INTO evidence_sources (
        project_id, dataset_id, source_type, source_id, source_version,
        visibility, content_hash, index_status, metadata, deleted_at
    )
    SELECT
        n.project_id,
        d.id,
        'note_comment',
        NEW.id,
        NEW.content_version,
        n.visibility,
        encode(digest(concat_ws(E'\x1f', NEW.content, NEW.status, COALESCE(NEW.parent_id::text, '')), 'sha256'), 'hex'),
        CASE
            WHEN n.deleted_at IS NULL AND n.status <> 'deleted' AND NEW.deleted_at IS NULL AND NEW.status = 1
            THEN 'pending' ELSE 'deleted'
        END,
        jsonb_build_object(
            'note_id', NEW.note_id,
            'user_id', NEW.user_id,
            'parent_id', NEW.parent_id,
            'content_version', NEW.content_version
        ),
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
        index_status = CASE WHEN EXCLUDED.deleted_at IS NULL THEN evidence_sources.index_status ELSE 'deleted' END,
        metadata = EXCLUDED.metadata,
        deleted_at = EXCLUDED.deleted_at,
        updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_note_comments_evidence_source ON note_comments;
CREATE TRIGGER trg_note_comments_evidence_source
AFTER INSERT OR UPDATE OF content, status, parent_id, deleted_at, sentiment, intent, topic_id, content_version OR DELETE ON note_comments
FOR EACH ROW EXECUTE FUNCTION sync_note_comment_evidence_source();

CREATE TABLE IF NOT EXISTS dataset_versions (
    id BIGSERIAL PRIMARY KEY,
    dataset_id BIGINT NOT NULL REFERENCES datasets(id) ON DELETE RESTRICT,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    version BIGINT NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'building'
        CHECK (status IN ('building', 'frozen')),
    source_count BIGINT NOT NULL DEFAULT 0,
    manifest_checksum CHAR(64),
    created_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    frozen_at TIMESTAMPTZ,
    UNIQUE (dataset_id, version),
    CHECK ((status = 'building' AND frozen_at IS NULL) OR
           (status = 'frozen' AND frozen_at IS NOT NULL AND manifest_checksum IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS dataset_version_sources (
    dataset_version_id BIGINT NOT NULL REFERENCES dataset_versions(id) ON DELETE RESTRICT,
    evidence_source_id BIGINT NOT NULL REFERENCES evidence_sources(id) ON DELETE RESTRICT,
    project_id BIGINT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    source_type VARCHAR(40) NOT NULL,
    source_id BIGINT NOT NULL,
    source_version BIGINT NOT NULL,
    content_hash CHAR(64) NOT NULL,
    visibility VARCHAR(24) NOT NULL CHECK (visibility IN ('public', 'project')),
    PRIMARY KEY (dataset_version_id, evidence_source_id),
    UNIQUE (dataset_version_id, source_type, source_id, source_version)
);

CREATE INDEX IF NOT EXISTS idx_dataset_version_sources_lookup
    ON dataset_version_sources(dataset_version_id, source_type, source_id, source_version);

CREATE OR REPLACE FUNCTION guard_frozen_dataset_membership()
RETURNS TRIGGER AS $$
DECLARE
    target_dataset_id BIGINT;
    target_status VARCHAR(24);
BEGIN
    target_dataset_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.dataset_id ELSE NEW.dataset_id END;
    SELECT status INTO target_status FROM datasets WHERE id = target_dataset_id;
    IF target_status = 'frozen' THEN
        RAISE EXCEPTION 'frozen dataset % membership is immutable', target_dataset_id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_frozen_dataset_membership ON dataset_notes;
CREATE TRIGGER trg_guard_frozen_dataset_membership
BEFORE INSERT OR UPDATE OR DELETE ON dataset_notes
FOR EACH ROW EXECUTE FUNCTION guard_frozen_dataset_membership();

CREATE OR REPLACE FUNCTION guard_frozen_dataset_version()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.status = 'frozen' THEN
        RAISE EXCEPTION 'frozen dataset version % is immutable', OLD.id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_frozen_dataset_version ON dataset_versions;
CREATE TRIGGER trg_guard_frozen_dataset_version
BEFORE UPDATE OR DELETE ON dataset_versions
FOR EACH ROW EXECUTE FUNCTION guard_frozen_dataset_version();

CREATE OR REPLACE FUNCTION guard_frozen_dataset_version_source()
RETURNS TRIGGER AS $$
DECLARE
    target_version_id BIGINT;
    target_status VARCHAR(24);
BEGIN
    target_version_id := CASE WHEN TG_OP = 'DELETE' THEN OLD.dataset_version_id ELSE NEW.dataset_version_id END;
    SELECT status INTO target_status FROM dataset_versions WHERE id = target_version_id;
    IF target_status = 'frozen' THEN
        RAISE EXCEPTION 'sources for frozen dataset version % are immutable', target_version_id;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_guard_frozen_dataset_version_source ON dataset_version_sources;
CREATE TRIGGER trg_guard_frozen_dataset_version_source
BEFORE INSERT OR UPDATE OR DELETE ON dataset_version_sources
FOR EACH ROW EXECUTE FUNCTION guard_frozen_dataset_version_source();
