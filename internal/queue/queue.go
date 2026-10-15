// Package queue persists download jobs in SQLite so they survive restarts.
// An album job is expanded into track jobs (children); tracks are
// downloaded one by one. Failed attempts are retried after a delay.
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"your-music-library/internal/cleanup"
	"your-music-library/internal/source"
)

// Status of a job.
type Status string

const (
	Pending Status = "pending"
	Running Status = "running"
	Done    Status = "done"
	Failed  Status = "failed"
)

// MaxAttempts is how often a job is tried before it fails for good.
const MaxAttempts = 3

// Job is one queued album or track.
type Job struct {
	ID        int64
	Source    string
	SourceID  string
	Kind      source.Kind
	ParentID  int64 // album job of a track, 0 for standalone jobs
	Status    Status
	Attempts  int
	NotBefore time.Time
	LastError string
	Note      string
	File      string // downloaded/imported file, relative to the library
	Item      source.Item
	Created   time.Time
	Updated   time.Time
}

// Ref is the job's "<source>:<id>".
func (j Job) Ref() source.Ref { return source.Ref{Source: j.Source, ID: j.SourceID} }

// Queue is the job store.
type Queue struct {
	db  *sql.DB
	now func() time.Time
}

// New returns a Queue on db.
func New(db *sql.DB) *Queue { return &Queue{db: db, now: time.Now} }

const columns = `id, source, source_id, kind, coalesce(parent_id, 0), status, attempts, not_before,
	last_error, note, file, payload, created_at, updated_at`

type scanner interface{ Scan(...any) error }

func scanJob(s scanner) (Job, error) {
	var j Job
	var payload string
	var notBefore, created, updated int64
	if err := s.Scan(&j.ID, &j.Source, &j.SourceID, &j.Kind, &j.ParentID, &j.Status, &j.Attempts, &notBefore,
		&j.LastError, &j.Note, &j.File, &payload, &created, &updated); err != nil {
		return Job{}, err
	}
	j.NotBefore, j.Created, j.Updated = time.Unix(notBefore, 0), time.Unix(created, 0), time.Unix(updated, 0)
	if err := json.Unmarshal([]byte(payload), &j.Item); err != nil {
		return Job{}, err
	}
	return j, nil
}

// Enqueue adds a job. If the same source item is already queued, the
// existing job is returned (created=false); a failed one is reset so it
// runs again.
func (q *Queue) Enqueue(ctx context.Context, src string, kind source.Kind, item source.Item, parent int64) (Job, bool, error) {
	payload, err := json.Marshal(item)
	if err != nil {
		return Job{}, false, err
	}
	now := q.now().Unix()
	var parentArg any
	if parent > 0 {
		parentArg = parent
	}
	res, err := q.db.ExecContext(ctx, `INSERT INTO queue_jobs (source, source_id, kind, parent_id, payload, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (source, kind, source_id) DO NOTHING`,
		src, item.ID, kind, parentArg, string(payload), now, now)
	if err != nil {
		return Job{}, false, err
	}
	created, _ := res.RowsAffected()
	if created == 0 {
		if _, err := q.db.ExecContext(ctx, `UPDATE queue_jobs SET status = 'pending', attempts = 0, not_before = 0,
			last_error = '', parent_id = coalesce(?, parent_id), updated_at = ?
			WHERE source = ? AND kind = ? AND source_id = ? AND status = 'failed'`,
			parentArg, now, src, kind, item.ID); err != nil {
			return Job{}, false, err
		}
	}
	j, err := scanJob(q.db.QueryRowContext(ctx, "SELECT "+columns+" FROM queue_jobs WHERE source = ? AND kind = ? AND source_id = ?", src, kind, item.ID))
	return j, created > 0, err
}

// Claim marks the oldest ready pending job of kind as running.
func (q *Queue) Claim(ctx context.Context, kind source.Kind) (Job, bool, error) {
	now := q.now().Unix()
	j, err := scanJob(q.db.QueryRowContext(ctx, `UPDATE queue_jobs SET status = 'running', updated_at = ?
		WHERE id = (SELECT id FROM queue_jobs WHERE status = 'pending' AND kind = ? AND not_before <= ? ORDER BY id LIMIT 1)
		RETURNING `+columns, now, kind, now))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	return j, err == nil, err
}

// Get returns a job by id.
func (q *Queue) Get(ctx context.Context, id int64) (Job, error) {
	return scanJob(q.db.QueryRowContext(ctx, "SELECT "+columns+" FROM queue_jobs WHERE id = ?", id))
}

// Complete marks a job done with a note and file.
func (q *Queue) Complete(ctx context.Context, id int64, note, file string) error {
	_, err := q.db.ExecContext(ctx, `UPDATE queue_jobs SET status = 'done', note = ?, file = ?, last_error = '', updated_at = ? WHERE id = ?`,
		note, file, q.now().Unix(), id)
	return err
}

// SetItem replaces a job's item (after resolving it with the source).
func (q *Queue) SetItem(ctx context.Context, id int64, item source.Item) error {
	payload, err := json.Marshal(item)
	if err != nil {
		return err
	}
	_, err = q.db.ExecContext(ctx, `UPDATE queue_jobs SET payload = ?, updated_at = ? WHERE id = ?`, string(payload), q.now().Unix(), id)
	return err
}

// SetFile records a job's file without changing its status.
func (q *Queue) SetFile(ctx context.Context, id int64, note, file string) error {
	_, err := q.db.ExecContext(ctx, `UPDATE queue_jobs SET note = ?, file = ?, updated_at = ? WHERE id = ?`, note, file, q.now().Unix(), id)
	return err
}

// Fail records an attempt's error. Retryable errors put the job back as
// pending after backoff until MaxAttempts is reached.
func (q *Queue) Fail(ctx context.Context, id int64, cause error, retryable bool, backoff time.Duration) (Status, error) {
	j, err := q.Get(ctx, id)
	if err != nil {
		return "", err
	}
	attempts := j.Attempts + 1
	status, notBefore := Failed, int64(0)
	if retryable && attempts < MaxAttempts {
		status, notBefore = Pending, q.now().Add(backoff).Unix()
	}
	_, err = q.db.ExecContext(ctx, `UPDATE queue_jobs SET status = ?, attempts = ?, not_before = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		status, attempts, notBefore, cause.Error(), q.now().Unix(), id)
	return status, err
}

// List returns jobs: albums first with their tracks after them, then
// standalone tracks, oldest first.
func (q *Queue) List(ctx context.Context) ([]Job, error) {
	return q.query(ctx, "SELECT "+columns+" FROM queue_jobs ORDER BY coalesce(parent_id, id), parent_id IS NOT NULL, id")
}

// Children returns the track jobs of an album job.
func (q *Queue) Children(ctx context.Context, parent int64) ([]Job, error) {
	return q.query(ctx, "SELECT "+columns+" FROM queue_jobs WHERE parent_id = ? ORDER BY id", parent)
}

// Running returns jobs of kind marked running.
func (q *Queue) Running(ctx context.Context, kind source.Kind) ([]Job, error) {
	return q.query(ctx, "SELECT "+columns+" FROM queue_jobs WHERE status = 'running' AND kind = ? ORDER BY id", kind)
}

func (q *Queue) query(ctx context.Context, query string, args ...any) (_ []Job, err error) {
	rows, err := q.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer cleanup.Close(&err, rows)
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// Recover puts jobs left running by a crash back to pending. Album jobs
// stay running: their tracks decide when they are done.
func (q *Queue) Recover(ctx context.Context) (int, error) {
	res, err := q.db.ExecContext(ctx, "UPDATE queue_jobs SET status = 'pending', updated_at = ? WHERE status = 'running' AND kind = 'track'", q.now().Unix())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// NextWake returns when the earliest delayed pending job becomes ready.
func (q *Queue) NextWake(ctx context.Context) (time.Time, bool, error) {
	var t sql.NullInt64
	err := q.db.QueryRowContext(ctx, "SELECT min(not_before) FROM queue_jobs WHERE status = 'pending'").Scan(&t)
	if err != nil || !t.Valid {
		return time.Time{}, false, err
	}
	return time.Unix(t.Int64, 0), true, nil
}

// Retry resets failed jobs (one id, or all when id is 0) to pending.
func (q *Queue) Retry(ctx context.Context, id int64) (int, error) {
	query := "UPDATE queue_jobs SET status = 'pending', attempts = 0, not_before = 0, last_error = '', updated_at = ? WHERE status = 'failed'"
	args := []any{q.now().Unix()}
	if id > 0 {
		query += " AND (id = ? OR parent_id = ?)"
		args = append(args, id, id)
	}
	res, err := q.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	// albums whose tracks are retried have to run (and ingest) again
	if _, err := q.db.ExecContext(ctx, `UPDATE queue_jobs SET status = 'running'
		WHERE kind = 'album' AND status = 'done' AND id IN (SELECT parent_id FROM queue_jobs WHERE status = 'pending')`); err != nil {
		return int(n), err
	}
	return int(n), nil
}

// Clear deletes finished jobs (done, and failed if includeFailed).
// Albums are deleted only together with all their tracks.
func (q *Queue) Clear(ctx context.Context, includeFailed bool) (int, error) {
	finished := "('done')"
	if includeFailed {
		finished = "('done', 'failed')"
	}
	// tracks first: deleting an album would cascade to its tracks, and
	// cascaded rows are not counted
	tracks, err := q.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM queue_jobs WHERE kind = 'track' AND status IN %[1]s
		AND (parent_id IS NULL OR parent_id IN (SELECT id FROM queue_jobs a WHERE a.status IN %[1]s
			AND NOT EXISTS (SELECT 1 FROM queue_jobs c WHERE c.parent_id = a.id AND c.status NOT IN %[1]s)))`, finished))
	if err != nil {
		return 0, err
	}
	albums, err := q.db.ExecContext(ctx, fmt.Sprintf(`DELETE FROM queue_jobs WHERE kind = 'album' AND status IN %[1]s
		AND NOT EXISTS (SELECT 1 FROM queue_jobs c WHERE c.parent_id = queue_jobs.id)`, finished))
	if err != nil {
		return 0, err
	}
	nt, _ := tracks.RowsAffected()
	na, _ := albums.RowsAffected()
	n := nt + na
	return int(n), nil
}
