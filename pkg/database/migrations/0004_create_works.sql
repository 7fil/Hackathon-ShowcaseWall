CREATE TABLE works (
    id                 UUID PRIMARY KEY,
    owner_id           UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title              TEXT        NOT NULL,
    summary            TEXT        NOT NULL,
    description        TEXT        NOT NULL,
    benefits           TEXT        NOT NULL,
    category           TEXT        NOT NULL,
    tech_stack         TEXT[]      NOT NULL DEFAULT '{}',
    team_members       TEXT[]      NOT NULL DEFAULT '{}',
    demo_url           TEXT,
    repo_url           TEXT,
    status             TEXT        NOT NULL DEFAULT 'Draft',
    is_featured        BOOLEAN     NOT NULL DEFAULT FALSE,
    cover_key          TEXT        NOT NULL,
    cover_content_type TEXT        NOT NULL,
    cover_size_bytes   BIGINT      NOT NULL,
    published_at       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at         TIMESTAMPTZ,
    CONSTRAINT works_status_valid CHECK (status IN ('Draft', 'Published', 'Unpublished')),
    CONSTRAINT works_featured_only_published CHECK (NOT is_featured OR status = 'Published')
);

CREATE UNIQUE INDEX works_single_featured_idx
    ON works (is_featured)
    WHERE is_featured = TRUE AND deleted_at IS NULL;

CREATE INDEX works_public_gallery_idx
    ON works (published_at DESC, id DESC)
    WHERE status = 'Published' AND deleted_at IS NULL;

CREATE INDEX works_exhibition_title_idx
    ON works (lower(title) ASC, id ASC)
    WHERE status = 'Published' AND deleted_at IS NULL;

CREATE INDEX works_owner_created_idx
    ON works (owner_id, created_at DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX works_tech_stack_gin_idx
    ON works USING GIN (tech_stack)
    WHERE status = 'Published' AND deleted_at IS NULL;
