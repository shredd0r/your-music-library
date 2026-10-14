-- Index of library files. File tags are the source of truth; every row can
-- be rebuilt by `ymlib scan`.
CREATE TABLE tracks (
    id                 INTEGER PRIMARY KEY,
    path               TEXT    NOT NULL UNIQUE, -- relative to library_path
    size               INTEGER NOT NULL,
    mtime_ns           INTEGER NOT NULL,
    title              TEXT    NOT NULL DEFAULT '',
    album              TEXT    NOT NULL DEFAULT '',
    artist             TEXT    NOT NULL DEFAULT '',
    artists            TEXT    NOT NULL DEFAULT '[]', -- JSON array
    album_artist       TEXT    NOT NULL DEFAULT '',
    date               TEXT    NOT NULL DEFAULT '',   -- YYYY, YYYY-MM or YYYY-MM-DD
    track_number       INTEGER NOT NULL DEFAULT 0,
    disc_number        INTEGER NOT NULL DEFAULT 0,
    genre              TEXT    NOT NULL DEFAULT '',
    has_cover          INTEGER NOT NULL DEFAULT 0,
    -- identity, used for "already downloaded" detection
    source             TEXT    NOT NULL DEFAULT '',
    source_track_id    TEXT    NOT NULL DEFAULT '',
    isrc               TEXT    NOT NULL DEFAULT '',
    mb_recording_id    TEXT    NOT NULL DEFAULT '',
    mb_release_id      TEXT    NOT NULL DEFAULT '',
    discogs_release_id TEXT    NOT NULL DEFAULT '',
    indexed_at         INTEGER NOT NULL -- unix seconds
);
CREATE INDEX tracks_album     ON tracks (album_artist, album, disc_number, track_number);
CREATE INDEX tracks_source_id ON tracks (source, source_track_id) WHERE source_track_id <> '';
CREATE INDEX tracks_isrc      ON tracks (isrc) WHERE isrc <> '';
CREATE INDEX tracks_mb_rec    ON tracks (mb_recording_id) WHERE mb_recording_id <> '';

-- Persistent download queue; survives restarts.
CREATE TABLE queue_jobs (
    id          INTEGER PRIMARY KEY,
    source      TEXT    NOT NULL,
    source_id   TEXT    NOT NULL,
    kind        TEXT    NOT NULL CHECK (kind IN ('track', 'album')),
    status      TEXT    NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'running', 'done', 'failed', 'review')),
    attempts    INTEGER NOT NULL DEFAULT 0,
    last_error  TEXT    NOT NULL DEFAULT '',
    payload     TEXT    NOT NULL DEFAULT '{}', -- JSON: source search result
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    UNIQUE (source, source_id)
);
CREATE INDEX queue_jobs_status ON queue_jobs (status, created_at);

-- Low-confidence metadata matches waiting for the user.
CREATE TABLE review_items (
    id          INTEGER PRIMARY KEY,
    path        TEXT    NOT NULL, -- file in temp or library
    query       TEXT    NOT NULL, -- JSON: what was looked up
    candidates  TEXT    NOT NULL, -- JSON: scored candidate releases
    status      TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved', 'skipped')),
    created_at  INTEGER NOT NULL
);

-- Persistent half of the two-level provider cache.
CREATE TABLE cache_entries (
    key        TEXT    PRIMARY KEY,
    value      BLOB    NOT NULL,
    created_at INTEGER NOT NULL -- unix seconds; purged after cache.max_age_days
);
