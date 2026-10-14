// Package library indexes and reorganizes the music folder. File tags are
// the source of truth; the SQLite index is rebuilt from them by Scan.
// Operations that change files come as a Plan (preview) and an Apply step.
package library

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"your-music-library/internal/pathtmpl"
	"your-music-library/internal/tags"
)

// Library is a music folder plus its index.
type Library struct {
	root     string
	db       *sql.DB
	template *pathtmpl.Template
	now      func() time.Time
}

// Open returns a Library for root. root must be an existing directory.
func Open(root string, db *sql.DB, template string) (*Library, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("library folder: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("library folder %s is not a directory", root)
	}
	tp, err := pathtmpl.Parse(template)
	if err != nil {
		return nil, err
	}
	return &Library{root: root, db: db, template: tp, now: time.Now}, nil
}

// Root is the library folder.
func (l *Library) Root() string { return l.root }

func (l *Library) abs(rel string) string { return filepath.Join(l.root, filepath.FromSlash(rel)) }

// reindex re-reads one file and updates its index row.
func (l *Library) reindex(ctx context.Context, rel string) error {
	e, err := l.readEntry(rel)
	if err != nil {
		return err
	}
	return upsert(ctx, l.db, e, l.now())
}

func (l *Library) readEntry(rel string) (Entry, error) {
	abs := l.abs(rel)
	info, err := os.Stat(abs)
	if err != nil {
		return Entry{}, err
	}
	t, err := tags.Read(abs)
	if err != nil {
		return Entry{}, err
	}
	e := Entry{Path: rel, Size: info.Size(), MtimeNs: info.ModTime().UnixNano(), HasCover: t.Cover != nil, Track: t}
	e.Cover, e.Back = nil, nil // pictures are not stored in the index
	return e, nil
}
