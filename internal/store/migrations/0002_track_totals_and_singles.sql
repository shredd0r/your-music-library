-- Totals let organize detect multi-disc albums ({disc?}); release type and
-- original album/track let singles move between as-album and grouped modes.
ALTER TABLE tracks ADD COLUMN track_total    INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tracks ADD COLUMN disc_total     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tracks ADD COLUMN release_type   TEXT    NOT NULL DEFAULT '';
ALTER TABLE tracks ADD COLUMN original_album TEXT    NOT NULL DEFAULT '';
ALTER TABLE tracks ADD COLUMN original_track INTEGER NOT NULL DEFAULT 0;
