-- Extra screenshots of a work (Should Have). The cover stays on works.cover_key.
CREATE TABLE work_screenshots (
    id           UUID PRIMARY KEY,
    work_id      UUID        NOT NULL REFERENCES works (id) ON DELETE CASCADE,
    object_key   TEXT        NOT NULL,
    position     INTEGER     NOT NULL,
    content_type TEXT        NOT NULL,
    size_bytes   BIGINT      NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT work_screenshots_position_positive CHECK (position > 0)
);

CREATE UNIQUE INDEX work_screenshots_work_position_key ON work_screenshots (work_id, position);
