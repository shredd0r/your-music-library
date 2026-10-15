package library

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"

	"your-music-library/internal/cleanup"
	"your-music-library/internal/tags"
)

// FileError is a file that could not be processed.
type FileError struct {
	Path string
	Err  error
}

// ScanReport summarizes a scan.
type ScanReport struct {
	Added, Updated, Unchanged, Removed int
	Unsupported                        []string    // audio formats that cannot be tagged yet
	Failed                             []FileError // unreadable files; their rows are kept
}

// Scan walks the library folder and brings the index in line with the
// files. It never modifies files. Files whose size and modification time
// match the index are not re-read.
func (l *Library) Scan(ctx context.Context) (r ScanReport, err error) {
	existing, err := l.Entries(ctx)
	if err != nil {
		return r, err
	}
	known := make(map[string]Entry, len(existing))
	for _, e := range existing {
		known[e.Path] = e
	}
	seen := map[string]bool{}

	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer cleanup.Rollback(&err, tx)
	now := l.now()

	err = filepath.WalkDir(l.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		name := d.Name()
		// Hidden entries include macOS "._" files that appear on FAT drives.
		if path != l.root && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !tags.IsAudio(name) {
			return nil
		}
		rel, err := filepath.Rel(l.root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !tags.Supported(name) {
			r.Unsupported = append(r.Unsupported, rel)
			return nil
		}
		seen[rel] = true

		info, err := d.Info()
		if err != nil {
			r.Failed = append(r.Failed, FileError{rel, err})
			return nil
		}
		old, ok := known[rel]
		if ok && old.Size == info.Size() && old.MtimeNs == info.ModTime().UnixNano() {
			r.Unchanged++
			return nil
		}
		e, err := l.readEntry(rel)
		if err != nil {
			r.Failed = append(r.Failed, FileError{rel, err})
			return nil
		}
		if err := upsert(ctx, tx, e, now); err != nil {
			return err
		}
		if ok {
			r.Updated++
		} else {
			r.Added++
		}
		return nil
	})
	if err != nil {
		return r, err
	}

	for path := range known {
		if !seen[path] {
			if _, err := tx.ExecContext(ctx, "DELETE FROM tracks WHERE path = ?", path); err != nil {
				return r, err
			}
			r.Removed++
		}
	}
	return r, tx.Commit()
}
