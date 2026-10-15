package queue

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"your-music-library/internal/source"
	"your-music-library/internal/store"
)

func newQueue(t *testing.T) (*Queue, *sql.DB, *time.Time) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Unix(1_000_000, 0)
	q := New(db)
	q.now = func() time.Time { return now }
	return q, db, &now
}

func TestEnqueueClaimComplete(t *testing.T) {
	q, _, _ := newQueue(t)
	ctx := context.Background()
	album, created, err := q.Enqueue(ctx, "fake", source.KindAlbum, source.Item{ID: "1", Title: "Bad"}, 0)
	if err != nil || !created || album.Status != Pending || album.Item.Title != "Bad" {
		t.Fatalf("enqueue = %+v %v %v", album, created, err)
	}
	// same id as a track is a different job
	track, created, _ := q.Enqueue(ctx, "fake", source.KindTrack, source.Item{ID: "1", Title: "Bad (song)"}, album.ID)
	if !created || track.ParentID != album.ID {
		t.Fatalf("track = %+v", track)
	}
	if _, created, _ := q.Enqueue(ctx, "fake", source.KindTrack, source.Item{ID: "1"}, album.ID); created {
		t.Error("duplicate enqueue created a job")
	}

	j, ok, err := q.Claim(ctx, source.KindTrack)
	if err != nil || !ok || j.ID != track.ID || j.Status != Running {
		t.Fatalf("claim = %+v %v %v", j, ok, err)
	}
	if _, ok, _ := q.Claim(ctx, source.KindTrack); ok {
		t.Error("claimed a running job twice")
	}
	q.Complete(ctx, j.ID, "imported", "MJ/Bad/01 - Bad.flac")
	j, _ = q.Get(ctx, j.ID)
	if j.Status != Done || j.File != "MJ/Bad/01 - Bad.flac" || j.Note != "imported" {
		t.Errorf("after complete = %+v", j)
	}
}

func TestFailRetriesWithBackoffThenFails(t *testing.T) {
	q, _, now := newQueue(t)
	ctx := context.Background()
	j, _, _ := q.Enqueue(ctx, "fake", source.KindTrack, source.Item{ID: "t"}, 0)
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		c, ok, _ := q.Claim(ctx, source.KindTrack)
		if !ok {
			t.Fatalf("attempt %d: not claimable", attempt)
		}
		st, _ := q.Fail(ctx, c.ID, errors.New("timeout"), true, time.Minute)
		if attempt < MaxAttempts {
			if st != Pending {
				t.Fatalf("attempt %d: status %s", attempt, st)
			}
			if _, ok, _ := q.Claim(ctx, source.KindTrack); ok {
				t.Fatal("claimed before backoff elapsed")
			}
			wake, ok, _ := q.NextWake(ctx)
			if !ok || !wake.Equal(now.Add(time.Minute)) {
				t.Errorf("NextWake = %v %v", wake, ok)
			}
			*now = now.Add(time.Minute)
		} else if st != Failed {
			t.Fatalf("final status %s", st)
		}
	}
	j, _ = q.Get(ctx, j.ID)
	if j.Attempts != MaxAttempts || j.LastError != "timeout" {
		t.Errorf("job = %+v", j)
	}
	if n, _ := q.Retry(ctx, 0); n != 1 {
		t.Errorf("Retry reset %d", n)
	}
	if _, ok, _ := q.Claim(ctx, source.KindTrack); !ok {
		t.Error("retried job not claimable")
	}
}

func TestPermanentFailureAndReenqueue(t *testing.T) {
	q, _, _ := newQueue(t)
	ctx := context.Background()
	q.Enqueue(ctx, "fake", source.KindTrack, source.Item{ID: "t"}, 0)
	c, _, _ := q.Claim(ctx, source.KindTrack)
	if st, _ := q.Fail(ctx, c.ID, errors.New("unsupported format"), false, 0); st != Failed {
		t.Fatalf("status %s", st)
	}
	j, created, _ := q.Enqueue(ctx, "fake", source.KindTrack, source.Item{ID: "t"}, 0)
	if created || j.Status != Pending || j.Attempts != 0 {
		t.Errorf("re-enqueue of failed job = %+v", j)
	}
}

func TestRecoverAndClear(t *testing.T) {
	q, _, _ := newQueue(t)
	ctx := context.Background()
	a, _, _ := q.Enqueue(ctx, "fake", source.KindAlbum, source.Item{ID: "a"}, 0)
	q.Claim(ctx, source.KindAlbum)
	t1, _, _ := q.Enqueue(ctx, "fake", source.KindTrack, source.Item{ID: "1"}, a.ID)
	q.Enqueue(ctx, "fake", source.KindTrack, source.Item{ID: "2"}, a.ID)
	q.Claim(ctx, source.KindTrack)

	if n, _ := q.Recover(ctx); n != 1 {
		t.Errorf("recovered %d", n)
	}
	if j, _ := q.Get(ctx, a.ID); j.Status != Running {
		t.Error("album jobs must stay running")
	}
	q.Complete(ctx, t1.ID, "", "")
	if n, _ := q.Clear(ctx, false); n != 0 {
		t.Errorf("cleared %d jobs of an unfinished album", n)
	}
	jobs, _ := q.List(ctx)
	if len(jobs) != 3 || jobs[0].ID != a.ID || jobs[1].ParentID != a.ID {
		t.Errorf("List order = %+v", jobs)
	}
	for _, j := range jobs {
		q.Complete(ctx, j.ID, "", "")
	}
	if n, _ := q.Clear(ctx, false); n != 3 {
		t.Errorf("cleared %d, want 3", n)
	}
}
