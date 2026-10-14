-- A review item covers a group of files identified together (an album).
-- `path` keeps the album folder for display; `paths` lists the files.
ALTER TABLE review_items ADD COLUMN paths TEXT NOT NULL DEFAULT '[]'; -- JSON array, relative to library_path
