-- Rebuild queue_jobs: the same id may name an album and a track in one
-- source, so uniqueness includes kind. Album jobs expand into track jobs
-- (parent_id); failed jobs wait until not_before before retrying.
CREATE TABLE queue_jobs_v2 (
    id          INTEGER PRIMARY KEY,
    source      TEXT    NOT NULL,
    source_id   TEXT    NOT NULL,
    kind        TEXT    NOT NULL CHECK (kind IN ('track', 'album')),
    parent_id   INTEGER REFERENCES queue_jobs_v2 (id) ON DELETE CASCADE,
    status      TEXT    NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'running', 'done', 'failed')),
    attempts    INTEGER NOT NULL DEFAULT 0,
    not_before  INTEGER NOT NULL DEFAULT 0, -- unix seconds
    last_error  TEXT    NOT NULL DEFAULT '',
    note        TEXT    NOT NULL DEFAULT '', -- outcome shown to the user
    file        TEXT    NOT NULL DEFAULT '', -- downloaded file, relative to library_path
    payload     TEXT    NOT NULL DEFAULT '{}', -- JSON: source item
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE (source, kind, source_id)
);
INSERT INTO queue_jobs_v2 (id, source, source_id, kind, status, attempts, last_error, payload, created_at, updated_at)
    SELECT id, source, source_id, kind, CASE status WHEN 'review' THEN 'done' ELSE status END,
           attempts, last_error, payload, created_at, updated_at
    FROM queue_jobs;
DROP TABLE queue_jobs;
ALTER TABLE queue_jobs_v2 RENAME TO queue_jobs;
CREATE INDEX queue_jobs_status ON queue_jobs (status, kind, not_before);
CREATE INDEX queue_jobs_parent ON queue_jobs (parent_id);
