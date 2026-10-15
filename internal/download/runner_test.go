package download

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"your-music-library/internal/config"
	"your-music-library/internal/library"
	"your-music-library/internal/queue"
	"your-music-library/internal/source"
	"your-music-library/internal/store"
	"your-music-library/internal/tags"
	"your-music-library/internal/testutil"
)

type env struct {
	runner *Runner
	lib    *library.Library
	q      *queue.Queue
	db     *sql.DB
	logs   []string
}

func setup(t *testing.T) *env {
	t.Helper()
	root := filepath.Join(t.TempDir(), "music")
	os.MkdirAll(root, 0o755)
	db, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	lib, err := library.Open(root, db, config.DefaultPathTemplate)
	if err != nil {
		t.Fatal(err)
	}
	plugins, err := source.Discover(testutil.FakeSourceDir(t))
	if err != nil {
		t.Fatal(err)
	}
	e := &env{lib: lib, q: queue.New(db), db: db}
	var mu sync.Mutex
	e.runner = &Runner{
		Queue: e.q, Sources: source.NewRegistry(plugins), Lib: lib,
		Preset: config.Default().Presets["full"], Parallel: 2,
		Backoff: func(int) time.Duration { return 0 },
		Log: func(format string, args ...any) {
			mu.Lock()
			e.logs = append(e.logs, strings.TrimSpace(sprintf(format, args...)))
			mu.Unlock()
		},
	}
	return e
}

func (e *env) enqueue(t *testing.T, kind source.Kind, id string) queue.Job {
	t.Helper()
	j, _, err := e.q.Enqueue(context.Background(), "fake", kind, source.Item{ID: id}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func (e *env) run(t *testing.T) Summary {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s, err := e.runner.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v\nlog:\n%s", err, strings.Join(e.logs, "\n"))
	}
	return s
}

func (e *env) path(rel string) string { return filepath.Join(e.lib.Root(), filepath.FromSlash(rel)) }

func TestAlbumDownload(t *testing.T) {
	e := setup(t)
	album := e.enqueue(t, source.KindAlbum, "a1")
	sum := e.run(t)
	if sum.Imported != 2 || sum.Failed != 0 {
		t.Fatalf("summary = %+v\n%s", sum, strings.Join(e.logs, "\n"))
	}
	got, err := tags.Read(e.path("Michael Jackson/Off the Wall/02 - Rock With You.flac"))
	if err != nil {
		t.Fatal(err)
	}
	if got.IDs.Source != "fake" || got.IDs.SourceTrackID != "t1" || got.IDs.ISRC != "USSM17900001" ||
		got.AlbumArtist != "Michael Jackson" || got.Date != "1979" {
		t.Errorf("tags = %+v", got)
	}
	if _, err := os.Stat(e.path("Michael Jackson/Off the Wall/05 - Off the Wall.flac")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(e.path(library.StagingDir)); !os.IsNotExist(err) {
		t.Error("staging folder left behind")
	}
	j, _ := e.q.Get(context.Background(), album.ID)
	if j.Status != queue.Done || j.Note != "2 imported, 0 already in library, 0 failed" {
		t.Errorf("album job = %+v", j)
	}
	children, _ := e.q.Children(context.Background(), album.ID)
	for _, c := range children {
		if c.Status != queue.Done || c.Note != "imported" || !strings.HasPrefix(c.File, "Michael Jackson/") {
			t.Errorf("track job = %+v", c)
		}
	}
	// the index knows the new files
	if r, _ := e.lib.Scan(context.Background()); r.Unchanged != 2 || r.Added != 0 {
		t.Errorf("scan after download = %+v", r)
	}
}

func TestSkipsTracksAlreadyInLibrary(t *testing.T) {
	e := setup(t)
	p := testutil.CopyAudio(t, "flac", e.path("old/rwy.flac"))
	tags.Write(p, tags.Track{Title: "Rock With You", Artist: "Michael Jackson", Album: "Off the Wall"})
	e.lib.Scan(context.Background())

	e.enqueue(t, source.KindAlbum, "a1")
	sum := e.run(t)
	if sum.Imported != 1 || sum.Skipped != 1 {
		t.Errorf("summary = %+v\n%s", sum, strings.Join(e.logs, "\n"))
	}
}

func TestRetryAfterTemporaryFailure(t *testing.T) {
	e := setup(t)
	t.Setenv("YMLIB_FAKE_FAIL_ONCE", t.TempDir())
	j := e.enqueue(t, source.KindTrack, "s1")
	if sum := e.run(t); sum.Imported != 1 {
		t.Fatalf("summary = %+v\n%s", sum, strings.Join(e.logs, "\n"))
	}
	j, _ = e.q.Get(context.Background(), j.ID)
	if j.Attempts != 1 || j.Status != queue.Done || j.File != "Rick Astley/Never Gonna Give You Up/01 - Never Gonna Give You Up.mp3" {
		t.Errorf("job = %+v", j)
	}
}

func TestBadDownloadsFail(t *testing.T) {
	t.Run("truncated", func(t *testing.T) {
		e := setup(t)
		j := e.enqueue(t, source.KindTrack, "long")
		if sum := e.run(t); sum.Failed != 1 || sum.Imported != 0 {
			t.Fatalf("summary = %+v", sum)
		}
		j, _ = e.q.Get(context.Background(), j.ID)
		if j.Status != queue.Failed || j.Attempts != queue.MaxAttempts || !strings.Contains(j.LastError, "incomplete download") {
			t.Errorf("job = %+v", j)
		}
	})
	t.Run("corrupt", func(t *testing.T) {
		e := setup(t)
		t.Setenv("YMLIB_FAKE_CORRUPT", "1")
		j := e.enqueue(t, source.KindTrack, "t2")
		e.run(t)
		j, _ = e.q.Get(context.Background(), j.ID)
		if j.Status != queue.Failed || !strings.Contains(j.LastError, "not a valid audio file") {
			t.Errorf("job = %+v", j)
		}
		if entries, _ := os.ReadDir(e.lib.Root()); len(entries) != 0 {
			t.Errorf("library not empty after failed download: %v", entries)
		}
	})
	t.Run("unknown source", func(t *testing.T) {
		e := setup(t)
		j, _, _ := e.q.Enqueue(context.Background(), "nope", source.KindTrack, source.Item{ID: "x"}, 0)
		e.run(t)
		j, _ = e.q.Get(context.Background(), j.ID)
		if j.Status != queue.Failed || j.Attempts != 1 {
			t.Errorf("permanent error should not retry: %+v", j)
		}
	})
}

func TestVerifyUnsupportedIsPermanent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.m4a")
	os.WriteFile(p, []byte("x"), 0o644)
	if err := Verify(context.Background(), p, 0); !IsPermanent(err) {
		t.Errorf("Verify = %v", err)
	}
}
