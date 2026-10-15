package cleanup

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

type closer struct{ err error }

func (c closer) Close() error { return c.err }

func TestClose(t *testing.T) {
	var err error
	Close(&err, closer{})
	if err != nil {
		t.Errorf("clean close: %v", err)
	}
	boom := errors.New("boom")
	Close(&err, closer{boom})
	if !errors.Is(err, boom) {
		t.Errorf("close error lost: %v", err)
	}
	first := errors.New("first")
	err = first
	Close(&err, closer{boom})
	if !errors.Is(err, first) || !errors.Is(err, boom) {
		t.Errorf("both errors expected: %v", err)
	}
}

func TestRollbackAfterCommit(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, _ := db.Begin()
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var e error
	Rollback(&e, tx)
	if e != nil {
		t.Errorf("rollback after commit reported: %v", e)
	}
}

func TestRemoveHelpers(t *testing.T) {
	dir := t.TempDir()
	if err := Remove(filepath.Join(dir, "missing")); err != nil {
		t.Errorf("Remove missing: %v", err)
	}
	full := filepath.Join(dir, "full")
	os.MkdirAll(full, 0o755)
	os.WriteFile(filepath.Join(full, "x"), nil, 0o644)
	if err := RemoveIfEmpty(full); err != nil {
		t.Errorf("RemoveIfEmpty non-empty: %v", err)
	}
	if _, err := os.Stat(full); err != nil {
		t.Error("non-empty dir removed")
	}
	empty := filepath.Join(dir, "empty")
	os.MkdirAll(empty, 0o755)
	if err := RemoveIfEmpty(empty); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Error("empty dir not removed")
	}
	if err := RemoveIfEmpty(filepath.Join(dir, "nope")); err != nil {
		t.Errorf("missing dir: %v", err)
	}
}
