package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"your-music-library/internal/config"
	"your-music-library/internal/store"
	"your-music-library/internal/tags"
	"your-music-library/internal/testutil"
)

// newLib returns a library in a temp folder with a fresh index.
func newLib(t *testing.T) *Library {
	t.Helper()
	root := filepath.Join(t.TempDir(), "music")
	os.MkdirAll(root, 0o755)
	db, err := store.Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	l, err := Open(root, db, config.DefaultPathTemplate)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// addFile creates a tagged audio file at rel inside the library.
func addFile(t *testing.T, l *Library, rel string, tr tags.Track) string {
	t.Helper()
	ext := filepath.Ext(rel)[1:]
	abs := testutil.CopyAudio(t, ext, l.abs(rel))
	if err := tags.Write(abs, tr); err != nil {
		t.Fatal(err)
	}
	return abs
}

func scan(t *testing.T, l *Library) ScanReport {
	t.Helper()
	r, err := l.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func entries(t *testing.T, l *Library) map[string]Entry {
	t.Helper()
	es, err := l.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Entry{}
	for _, e := range es {
		m[e.Path] = e
	}
	return m
}
