// Package cleanup handles errors from closing and removing things, so they
// are reported instead of dropped.
//
//	func f() (err error) {
//		file, err := os.Open(p)
//		...
//		defer cleanup.Close(&err, file)
package cleanup

import (
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"os"
)

// Close closes c and adds its error to *err.
func Close(err *error, c io.Closer) {
	*err = errors.Join(*err, c.Close())
}

// Rollback rolls tx back and adds its error to *err. After a successful
// Commit the rollback is a no-op (sql.ErrTxDone), which is not an error.
func Rollback(err *error, tx *sql.Tx) {
	if rerr := tx.Rollback(); !errors.Is(rerr, sql.ErrTxDone) {
		*err = errors.Join(*err, rerr)
	}
}

// Remove deletes a file; a file that is already gone is not an error.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// RemoveAll deletes a directory tree (a missing one is not an error).
func RemoveAll(path string) error { return os.RemoveAll(path) }

// RemoveIfEmpty deletes dir only when it has no entries; a missing or
// non-empty dir is not an error.
func RemoveIfEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return nil
	}
	return Remove(dir)
}
