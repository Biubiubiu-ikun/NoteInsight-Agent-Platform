DROP TABLE IF EXISTS danmus CASCADE;
DROP TABLE IF EXISTS comment_likes CASCADE;
DROP TABLE IF EXISTS comments CASCADE;
DROP TABLE IF EXISTS videos CASCADE;

CREATE TABLE IF NOT EXISTS users (
    id BIGINT PRIMARY KEY,
    username VARCHAR(64) NOT NULL,
    nickname VARCHAR(64),
    avatar_url VARCHAR(512),
    role VARCHAR(32) NOT NULL DEFAULT 'normal',
    persona VARCHAR(64),
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_users_role_created ON users(role, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_users_persona ON users(persona);

CREATE TABLE IF NOT EXISTS user_auth_tokens (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash VARCHAR(128) NOT NULL UNIQUE,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expired_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_user_tokens_user ON user_auth_tokens(user_id);

CREATE TABLE IF NOT EXISTS notes (
    id BIGSERIAL PRIMARY KEY,
    project_id BIGINT NOT NULL DEFAULT 0,
    author_id BIGINT NOT NULL REFERENCES users(id),
    title VARCHAR(255) NOT NULL,
    body TEXT NOT NULL,
    category VARCHAR(64) NOT NULL,
    topics JSONB NOT NULL DEFAULT '[]'::jsonb,
    tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    location JSONB NOT NULL DEFAULT '{}'::jsonb,
    product_entities JSONB NOT NULL DEFAULT '[]'::jsonb,
    note_type VARCHAR(32) NOT NULL DEFAULT 'image_text',
    view_count BIGINT NOT NULL DEFAULT 0,
    like_count BIGINT NOT NULL DEFAULT 0,
    collect_count BIGINT NOT NULL DEFAULT 0,
    comment_count BIGINT NOT NULL DEFAULT 0,
    share_count BIGINT NOT NULL DEFAULT 0,
    hot_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    quality_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'published',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_notes_project_created ON notes(project_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_notes_category_created ON notes(category, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_notes_author_created ON notes(author_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_notes_hot_score ON notes(hot_score DESC);
CREATE INDEX IF NOT EXISTS idx_notes_collect_count ON notes(collect_count DESC);

CREATE TABLE IF NOT EXISTS note_media (
    id BIGSERIAL PRIMARY KEY,
    note_id BIGINT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    media_type VARCHAR(32) NOT NULL,
    url VARCHAR(512),
    caption TEXT,
    ocr_text TEXT,
    position INT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_note_media_note_position ON note_media(note_id, position);

CREATE TABLE IF NOT EXISTS note_comments (
    id BIGSERIAL PRIMARY KEY,
    note_id BIGINT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id),
    parent_id BIGINT NOT NULL DEFAULT 0,
    root_id BIGINT NOT NULL DEFAULT 0,
    content TEXT NOT NULL,
    like_count BIGINT NOT NULL DEFAULT 0,
    reply_count BIGINT NOT NULL DEFAULT 0,
    sentiment VARCHAR(32),
    intent VARCHAR(64),
    topic_id BIGINT NOT NULL DEFAULT 0,
    status SMALLINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_note_comments_note_created ON note_comments(note_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_note_comments_note_like ON note_comments(note_id, like_count DESC);
CREATE INDEX IF NOT EXISTS idx_note_comments_parent_created ON note_comments(parent_id, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_note_comments_user_created ON note_comments(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_note_comments_intent ON note_comments(intent, created_at DESC);

CREATE TABLE IF NOT EXISTS note_likes (
    note_id BIGINT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (note_id, user_id)
);

CREATE TABLE IF NOT EXISTS note_collects (
    note_id BIGINT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    collection_name VARCHAR(128),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (note_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_note_collects_user_created ON note_collects(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS note_shares (
    id BIGSERIAL PRIMARY KEY,
    note_id BIGINT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_note_shares_note_created ON note_shares(note_id, created_at DESC);

CREATE TABLE IF NOT EXISTS note_comment_likes (
    comment_id BIGINT NOT NULL REFERENCES note_comments(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (comment_id, user_id)
);
