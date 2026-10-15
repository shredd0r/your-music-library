// Package cache keeps provider responses so repeated lookups cost no
// network requests. Store has two levels: an in-memory map used while the
// app runs, written back to SQLite on Flush (periodically and on exit).
// Files keeps larger blobs (cover images) as files on disk. Both drop
// entries older than the configured maximum age.
package cache

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"your-music-library/internal/cleanup"
)

type entry struct {
	value   []byte
	created time.Time
}

// Store is the two-level key/value cache. It is safe for concurrent use.
type Store struct {
	db     *sql.DB
	maxAge time.Duration
	now    func() time.Time

	mu    sync.Mutex
	mem   map[string]entry
	dirty map[string]bool
}

// Open returns a Store backed by the cache_entries table and deletes rows
// older than maxAge.
func Open(ctx context.Context, db *sql.DB, maxAge time.Duration) (*Store, error) {
	return open(ctx, db, maxAge, time.Now)
}

func open(ctx context.Context, db *sql.DB, maxAge time.Duration, now func() time.Time) (*Store, error) {
	s := &Store{db: db, maxAge: maxAge, now: now, mem: map[string]entry{}, dirty: map[string]bool{}}
	if _, err := db.ExecContext(ctx, "DELETE FROM cache_entries WHERE created_at < ?", s.cutoff().Unix()); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) cutoff() time.Time { return s.now().Add(-s.maxAge) }

// Get returns a cached value younger than maxAge. Misses in memory fall
// through to SQLite.
func (s *Store) Get(ctx context.Context, key string) ([]byte, bool) {
	s.mu.Lock()
	e, ok := s.mem[key]
	s.mu.Unlock()
	if ok {
		if e.created.Before(s.cutoff()) {
			return nil, false
		}
		return e.value, true
	}

	var value []byte
	var created int64
	err := s.db.QueryRowContext(ctx,
		"SELECT value, created_at FROM cache_entries WHERE key = ? AND created_at >= ?",
		key, s.cutoff().Unix()).Scan(&value, &created)
	if err != nil {
		return nil, false // sql.ErrNoRows or a read error: both are a miss
	}
	s.mu.Lock()
	if _, ok := s.mem[key]; !ok {
		s.mem[key] = entry{value: value, created: time.Unix(created, 0)}
	}
	s.mu.Unlock()
	return value, true
}

// Set stores value in memory; it reaches SQLite on the next Flush.
func (s *Store) Set(key string, value []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mem[key] = entry{value: value, created: s.now()}
	s.dirty[key] = true
}

// Flush writes entries set since the last flush to SQLite.
func (s *Store) Flush(ctx context.Context) error {
	s.mu.Lock()
	pending := make(map[string]entry, len(s.dirty))
	for k := range s.dirty {
		pending[k] = s.mem[k]
	}
	s.dirty = map[string]bool{}
	s.mu.Unlock()
	if len(pending) == 0 {
		return nil
	}

	err := s.write(ctx, pending)
	if err != nil {
		// keep them dirty so a later flush retries
		s.mu.Lock()
		for k := range pending {
			s.dirty[k] = true
		}
		s.mu.Unlock()
	}
	return err
}

func (s *Store) write(ctx context.Context, pending map[string]entry) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer cleanup.Rollback(&err, tx)
	for k, e := range pending {
		if _, err := tx.ExecContext(ctx,
			"INSERT OR REPLACE INTO cache_entries (key, value, created_at) VALUES (?, ?, ?)",
			k, e.value, e.created.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Run flushes every interval until ctx is done, then flushes once more.
// Start it in a goroutine; periodic flushes limit what a crash can lose.
func (s *Store) Run(ctx context.Context, interval time.Duration) error {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return s.Flush(context.WithoutCancel(ctx))
		case <-t.C:
			if err := s.Flush(ctx); err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
		}
	}
}
