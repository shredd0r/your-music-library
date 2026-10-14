package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Files caches blobs (cover images) as files named by the SHA-256 of
// their key, so large data stays out of SQLite.
type Files struct {
	dir    string
	maxAge time.Duration
	now    func() time.Time
}

// OpenFiles creates dir if needed and deletes files older than maxAge.
func OpenFiles(dir string, maxAge time.Duration) (*Files, error) {
	return openFiles(dir, maxAge, time.Now)
}

func openFiles(dir string, maxAge time.Duration, now func() time.Time) (*Files, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f := &Files{dir: dir, maxAge: maxAge, now: now}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		info, err := e.Info()
		if err == nil && !e.IsDir() && info.ModTime().Before(f.cutoff()) {
			os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	return f, nil
}

func (f *Files) cutoff() time.Time { return f.now().Add(-f.maxAge) }

func (f *Files) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(f.dir, hex.EncodeToString(sum[:]))
}

// Get returns the blob for key if present and younger than maxAge.
func (f *Files) Get(key string) ([]byte, bool) {
	p := f.path(key)
	info, err := os.Stat(p)
	if err != nil || info.ModTime().Before(f.cutoff()) {
		return nil, false
	}
	data, err := os.ReadFile(p)
	return data, err == nil
}

// Put stores data for key, replacing any previous blob atomically.
func (f *Files) Put(key string, data []byte) error {
	p := f.path(key)
	tmp, err := os.CreateTemp(f.dir, ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	now := f.now()
	return os.Chtimes(p, now, now)
}
