package store

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "library.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	v, err := SchemaVersion(db)
	if err != nil || v != LatestVersion() || v < 1 {
		t.Fatalf("version = %d, %v; latest %d", v, err, LatestVersion())
	}
	for _, table := range []string{"tracks", "queue_jobs", "review_items", "cache_entries"} {
		var n int
		if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n); err != nil || n != 1 {
			t.Errorf("table %s missing (%v)", table, err)
		}
	}
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d, %v", fk, err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO cache_entries (key, value, created_at) VALUES ('k', x'00', 1)"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db.Close()
	var n int
	db.QueryRow("SELECT count(*) FROM cache_entries").Scan(&n)
	if n != 1 {
		t.Errorf("data lost on reopen: %d rows", n)
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "library.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("PRAGMA user_version = 999")
	db.Close()

	if db, err := Open(path); err == nil {
		db.Close()
		t.Fatal("expected error for schema newer than binary")
	}
}

func TestQueueStatusConstraint(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "library.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO queue_jobs (source, source_id, kind, status, created_at, updated_at)
		VALUES ('fake', '1', 'track', 'exploded', 0, 0)`)
	if err == nil {
		t.Error("invalid status accepted")
	}
}
