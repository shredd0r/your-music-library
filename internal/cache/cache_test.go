package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"your-music-library/internal/store"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func openDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "library.db")
}

func TestStoreMemoryThenPersisted(t *testing.T) {
	ctx := context.Background()
	path := openDB(t)
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := &clock{time.Unix(1_000_000, 0)}
	s, err := open(ctx, db, 24*time.Hour, c.now)
	if err != nil {
		t.Fatal(err)
	}

	s.Set("k", []byte("v"))
	if v, ok := s.Get(ctx, "k"); !ok || string(v) != "v" {
		t.Fatalf("memory get = %q %v", v, ok)
	}
	var n int
	db.QueryRow("SELECT count(*) FROM cache_entries").Scan(&n)
	if n != 0 {
		t.Fatal("Set must not write to SQLite before Flush")
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	// a new Store (next app run) sees the flushed entry
	s2, _ := open(ctx, db, 24*time.Hour, c.now)
	if v, ok := s2.Get(ctx, "k"); !ok || string(v) != "v" {
		t.Errorf("persisted get = %q %v", v, ok)
	}
	if _, ok := s2.Get(ctx, "missing"); ok {
		t.Error("missing key found")
	}
}

func TestStoreExpiryAndPurge(t *testing.T) {
	ctx := context.Background()
	db, _ := store.Open(openDB(t))
	defer db.Close()
	c := &clock{time.Unix(1_000_000, 0)}
	s, _ := open(ctx, db, time.Hour, c.now)
	s.Set("old", []byte("1"))
	s.Flush(ctx)

	c.t = c.t.Add(2 * time.Hour)
	if _, ok := s.Get(ctx, "old"); ok {
		t.Error("expired entry returned from memory")
	}
	s2, _ := open(ctx, db, time.Hour, c.now) // startup purge
	var n int
	db.QueryRow("SELECT count(*) FROM cache_entries").Scan(&n)
	if n != 0 {
		t.Errorf("old rows not purged: %d", n)
	}
	if _, ok := s2.Get(ctx, "old"); ok {
		t.Error("purged entry returned")
	}
}

func TestRunFlushesPeriodicallyAndOnStop(t *testing.T) {
	db, _ := store.Open(openDB(t))
	defer db.Close()
	s, _ := Open(context.Background(), db, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- s.Run(ctx, 10*time.Millisecond) }()

	s.Set("a", []byte("1"))
	deadline := time.Now().Add(2 * time.Second)
	for {
		var n int
		db.QueryRow("SELECT count(*) FROM cache_entries").Scan(&n)
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("periodic flush did not happen")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Set("b", []byte("2"))
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow("SELECT count(*) FROM cache_entries").Scan(&n)
	if n != 2 {
		t.Errorf("final flush missing: %d rows", n)
	}
}

func TestFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "covers")
	c := &clock{time.Now()}
	f, err := openFiles(dir, time.Hour, c.now)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Put("https://example.com/a.jpg", []byte("img")); err != nil {
		t.Fatal(err)
	}
	if v, ok := f.Get("https://example.com/a.jpg"); !ok || string(v) != "img" {
		t.Errorf("get = %q %v", v, ok)
	}
	if _, ok := f.Get("other"); ok {
		t.Error("missing key found")
	}

	c.t = c.t.Add(2 * time.Hour)
	if _, ok := f.Get("https://example.com/a.jpg"); ok {
		t.Error("expired file returned")
	}
	openFiles(dir, time.Hour, c.now)
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("old files not purged: %d", len(entries))
	}
}
