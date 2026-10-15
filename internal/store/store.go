// Package store opens the SQLite database and applies schema migrations.
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"errors"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil {
			return nil, fmt.Errorf("migration %q: name must start with NNNN_", e.Name())
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	for i, m := range ms {
		if m.version != i+1 {
			return nil, fmt.Errorf("migrations must be numbered 1..n without gaps; got %s at position %d", m.name, i+1)
		}
	}
	return ms, nil
}

// Open opens (creating if needed) the database at path and migrates it to
// the latest schema. The schema version is kept in PRAGMA user_version.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, errors.Join(fmt.Errorf("migrating %s: %w", path, err), db.Close())
	}
	return db, nil
}

// SchemaVersion returns the database's current schema version.
func SchemaVersion(db *sql.DB) (int, error) {
	var v int
	err := db.QueryRow("PRAGMA user_version").Scan(&v)
	return v, err
}

// LatestVersion is the schema version this build migrates to.
func LatestVersion() int {
	ms, err := loadMigrations()
	if err != nil {
		panic(err) // embedded files are fixed at build time; covered by tests
	}
	return len(ms)
}

func migrate(db *sql.DB) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	current, err := SchemaVersion(db)
	if err != nil {
		return err
	}
	if current > len(ms) {
		return fmt.Errorf("database schema v%d is newer than this ymlib (v%d); upgrade ymlib", current, len(ms))
	}
	for _, m := range ms[current:] {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.sql); err != nil {
			return errors.Join(fmt.Errorf("%s: %w", m.name, err), tx.Rollback())
		}
		// PRAGMA cannot take bound parameters; version is an int we control.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			return errors.Join(err, tx.Rollback())
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
