// Package download runs the download queue: album jobs are resolved into
// track jobs, tracks are downloaded by source plugins into the library's
// staging folder, verified, tagged with the source's metadata and then
// ingested (identified, tagged with the preset, filed by the path
// template). Albums are ingested together once all their tracks finished.
package download

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"your-music-library/internal/cleanup"
	"your-music-library/internal/config"
	"your-music-library/internal/library"
	"your-music-library/internal/metadata"
	"your-music-library/internal/queue"
	"your-music-library/internal/source"
	"your-music-library/internal/tags"
)

// Runner processes the queue.
type Runner struct {
	Queue      *queue.Queue
	Sources    *source.Registry
	Lib        *library.Library
	Identifier *metadata.Identifier // nil: keep the source's tags
	Images     library.ImageSource  // nil: no cover downloads
	Preset     config.Preset
	Parallel   int
	Force      bool // download tracks that are already in the library
	Verify     Verifier
	// Backoff is the wait before retry number attempt (1-based).
	Backoff func(attempt int) time.Duration
	// Log receives one line per event.
	Log func(format string, args ...any)

	ingestMu sync.Mutex
}

// Summary counts outcomes of one Run.
type Summary struct {
	Imported, Skipped, Failed int
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log != nil {
		r.Log(format, args...)
	}
}

// warnCleanup logs a failed removal of temporary download files; the
// download itself is unaffected, but leftovers in staging should be seen.
func (r *Runner) warnCleanup(err error) {
	if err != nil {
		r.logf("! cleanup: %v", err)
	}
}

func (r *Runner) backoff(attempt int) time.Duration {
	if r.Backoff != nil {
		return r.Backoff(attempt)
	}
	return time.Duration(30<<(2*(attempt-1))) * time.Second // 30s, 2m, 8m
}

// Run works until no job is pending or running. Delayed retries are waited
// for. Cancel ctx to stop; unfinished jobs resume on the next Run.
func (r *Runner) Run(ctx context.Context) (Summary, error) {
	var sum Summary
	if r.Verify == nil {
		r.Verify = Verify
	}
	if _, err := r.Queue.Recover(ctx); err != nil {
		return sum, err
	}
	for {
		if err := r.expandAlbums(ctx); err != nil {
			return sum, err
		}
		if err := r.runTracks(ctx, &sum); err != nil {
			return sum, err
		}
		if err := r.finishAlbums(ctx, &sum); err != nil {
			return sum, err
		}
		wake, pending, err := r.Queue.NextWake(ctx)
		if err != nil {
			return sum, err
		}
		if !pending {
			r.warnCleanup(cleanup.RemoveIfEmpty(r.abs(library.StagingDir)))
			return sum, nil
		}
		if d := time.Until(wake); d > 0 {
			r.logf("waiting %s for the next retry", d.Round(time.Second))
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
				t.Stop()
				return sum, ctx.Err()
			case <-t.C:
			}
		}
	}
}

func (r *Runner) fail(ctx context.Context, j queue.Job, err error) error {
	st, ferr := r.Queue.Fail(ctx, j.ID, err, !IsPermanent(err), r.backoff(j.Attempts+1))
	if ferr != nil {
		return ferr
	}
	if st == queue.Pending {
		r.logf("! %s %s: %v (will retry)", j.Ref(), j.Item.Title, err)
	} else {
		r.logf("✗ %s %s: %v", j.Ref(), j.Item.Title, err)
	}
	return nil
}

func (r *Runner) expandAlbums(ctx context.Context) error {
	for {
		j, ok, err := r.Queue.Claim(ctx, source.KindAlbum)
		if err != nil || !ok {
			return err
		}
		p, err := r.Sources.Get(j.Source)
		if err != nil {
			if err := r.fail(ctx, j, Permanent(err)); err != nil {
				return err
			}
			continue
		}
		album, tracks, err := p.Resolve(ctx, j.SourceID, source.KindAlbum)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err := r.fail(ctx, j, err); err != nil {
				return err
			}
			continue
		}
		if album.ID == "" {
			album = j.Item
		}
		if err := r.Queue.SetItem(ctx, j.ID, album); err != nil {
			return err
		}
		for _, t := range tracks {
			fillFromAlbum(&t, album)
			if _, _, err := r.Queue.Enqueue(ctx, j.Source, source.KindTrack, t, j.ID); err != nil {
				return err
			}
		}
		if err := r.Queue.SetFile(ctx, j.ID, fmt.Sprintf("%d tracks", len(tracks)), ""); err != nil {
			return err
		}
		r.logf("+ %s %s — %s: %d tracks", j.Ref(), album.Artist, album.Title, len(tracks))
	}
}

func fillFromAlbum(t *source.Item, album source.Item) {
	if t.Album == "" {
		t.Album = album.Title
	}
	if t.AlbumArtist == "" {
		t.AlbumArtist = album.Artist
	}
	if t.Year == 0 {
		t.Year = album.Year
	}
	if t.CoverURL == "" {
		t.CoverURL = album.CoverURL
	}
}

func (r *Runner) runTracks(ctx context.Context, sum *Summary) error {
	workers := max(1, r.Parallel)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				j, ok, err := r.Queue.Claim(ctx, source.KindTrack)
				if err == nil && ok {
					err = r.processTrack(ctx, j, sum, &mu)
				}
				if err != nil || !ok {
					mu.Lock()
					if firstErr == nil && err != nil {
						firstErr = err
					}
					mu.Unlock()
					return
				}
			}
		}()
	}
	wg.Wait()
	if firstErr == nil {
		firstErr = ctx.Err()
	}
	return firstErr
}

func (r *Runner) jobDir(j queue.Job) string {
	return path.Join(library.StagingDir, "job-"+strconv.FormatInt(j.ID, 10))
}

func (r *Runner) abs(rel string) string { return filepath.Join(r.Lib.Root(), filepath.FromSlash(rel)) }

// processTrack downloads one track. Standalone tracks are ingested right
// away; album tracks wait in staging for finishAlbums.
func (r *Runner) processTrack(ctx context.Context, j queue.Job, sum *Summary, mu *sync.Mutex) error {
	count := func(f *int) { mu.Lock(); *f++; mu.Unlock() }
	p, err := r.Sources.Get(j.Source)
	if err != nil {
		count(&sum.Failed)
		return r.fail(ctx, j, Permanent(err))
	}
	if j.Item.Title == "" {
		// queued by reference only: ask the source for the metadata
		_, tracks, err := p.Resolve(ctx, j.SourceID, source.KindTrack)
		if err == nil && len(tracks) == 0 {
			err = Permanent(fmt.Errorf("source returned no track for %s", j.SourceID))
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if IsPermanent(err) || j.Attempts+1 >= queue.MaxAttempts {
				count(&sum.Failed)
			}
			return r.fail(ctx, j, err)
		}
		j.Item = tracks[0]
		if err := r.Queue.SetItem(ctx, j.ID, j.Item); err != nil {
			return err
		}
	}
	it := j.Item

	if !r.Force {
		r.ingestMu.Lock()
		idx, err := r.Lib.DupIndex(ctx)
		r.ingestMu.Unlock()
		if err != nil {
			return err
		}
		if st, p := idx.Check(library.DupQuery{Source: j.Source, SourceTrackID: it.ID, ISRC: it.ISRC,
			Artist: it.Artist, AlbumArtist: it.AlbumArtist, Album: it.Album, Title: it.Title}); st == library.InLibrary {
			r.logf("= %s %s: already in library (%s)", j.Ref(), it.Title, p)
			count(&sum.Skipped)
			return r.Queue.Complete(ctx, j.ID, "already in library", p)
		}
	}

	dir := r.jobDir(j)
	if err := os.MkdirAll(r.abs(dir), 0o755); err != nil {
		return err
	}
	r.logf("↓ %s %s — %s", j.Ref(), it.Artist, it.Title)
	file, err := p.Download(ctx, it.ID, r.abs(dir))
	if err == nil {
		err = r.Verify(ctx, file, it.Duration())
		if err != nil {
			r.warnCleanup(cleanup.Remove(file))
		}
	}
	if err == nil {
		err = r.writeSourceTags(ctx, file, j)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if st, _ := r.Queue.Get(ctx, j.ID); st.Attempts+1 >= queue.MaxAttempts || IsPermanent(err) {
			count(&sum.Failed)
		}
		r.warnCleanup(cleanup.RemoveAll(r.abs(dir)))
		return r.fail(ctx, j, err)
	}
	rel, _ := filepath.Rel(r.Lib.Root(), file)
	rel = filepath.ToSlash(rel)

	if j.ParentID != 0 {
		return r.Queue.Complete(ctx, j.ID, "downloaded", rel)
	}
	out := r.ingest(ctx, []string{file})[file]
	err = r.finishTrack(ctx, j, out, sum, mu)
	r.warnCleanup(cleanup.RemoveAll(r.abs(dir)))
	return err
}

// writeSourceTags merges the source's metadata into the file's tags, so
// the file can be recognized (source ID) and identified later.
func (r *Runner) writeSourceTags(ctx context.Context, file string, j queue.Job) error {
	t, err := tags.Read(file)
	if err != nil {
		return err
	}
	it := j.Item
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&t.Title, it.Title)
	set(&t.Artist, it.Artist)
	set(&t.Album, it.Album)
	set(&t.AlbumArtist, it.AlbumArtist)
	set(&t.IDs.ISRC, it.ISRC)
	if it.TrackNumber > 0 {
		t.TrackNumber = it.TrackNumber
	}
	if it.DiscNumber > 0 {
		t.DiscNumber = it.DiscNumber
	}
	if t.Date == "" && it.Year > 0 {
		t.Date = strconv.Itoa(it.Year)
	}
	t.IDs.Source, t.IDs.SourceTrackID = j.Source, it.ID
	if t.Cover == nil && it.CoverURL != "" && r.Images != nil {
		if data, mime, err := r.Images.Fetch(ctx, it.CoverURL); err == nil {
			t.Cover = &tags.Picture{MIME: mime, Data: data}
		} else {
			r.logf("! %s: cover: %v", j.Ref(), err)
		}
	}
	return tags.Write(file, t)
}

// outcome is what happened to one file during ingest.
type outcome struct {
	target string // library path when imported or already present
	dup    bool
	err    error
}

// ingest brings staged files into the library (serialized: it identifies,
// tags and indexes).
func (r *Runner) ingest(ctx context.Context, files []string) map[string]outcome {
	r.ingestMu.Lock()
	defer r.ingestMu.Unlock()
	out := map[string]outcome{}
	opts := library.IngestOptions{Base: r.abs(library.StagingDir), Force: r.Force, Move: true}
	plan, err := r.Lib.PlanIngest(ctx, files, r.Identifier, opts)
	if err != nil {
		for _, f := range files {
			out[f] = outcome{err: err}
		}
		return out
	}
	src := func(display string) string { return filepath.Join(opts.Base, filepath.FromSlash(display)) }
	for _, it := range plan.Duplicates {
		out[it.Source] = outcome{target: it.DupPath, dup: true}
		r.warnCleanup(cleanup.Remove(it.Source))
	}
	for _, c := range plan.Conflicts {
		out[src(c.From)] = outcome{err: fmt.Errorf("%s: %s (file kept in %s)", c.To, c.Reason, library.StagingDir)}
	}
	for _, f := range plan.Failed {
		out[src(f.Path)] = outcome{err: f.Err}
	}
	for _, u := range plan.Unsupported {
		out[src(u)] = outcome{err: Permanent(fmt.Errorf("unsupported format"))}
	}
	rep, err := r.Lib.ApplyIngest(ctx, plan, r.Preset, r.Images, opts)
	for _, w := range rep.Warnings {
		r.logf("! %s", w)
	}
	failed := map[string]error{}
	for _, f := range rep.Failed {
		failed[f.Path] = f.Err
	}
	for _, it := range plan.Items {
		switch {
		case failed[it.Display] != nil:
			out[it.Source] = outcome{err: failed[it.Display]}
		case err != nil:
			out[it.Source] = outcome{err: err}
		default:
			out[it.Source] = outcome{target: it.Target}
		}
	}
	if rep.Reviews > 0 {
		r.logf("? %d album(s) added to `ymlib review`", rep.Reviews)
	}
	return out
}

// finishTrack records an ingest outcome; a failed queue update is
// returned so the job's state is never silently wrong.
func (r *Runner) finishTrack(ctx context.Context, j queue.Job, o outcome, sum *Summary, mu *sync.Mutex) error {
	mu.Lock()
	defer mu.Unlock()
	switch {
	case o.err != nil:
		sum.Failed++
		r.logf("✗ %s %s: %v", j.Ref(), j.Item.Title, o.err)
		_, err := r.Queue.Fail(ctx, j.ID, Permanent(o.err), false, 0)
		return err
	case o.dup:
		sum.Skipped++
		r.logf("= %s %s: already in library (%s)", j.Ref(), j.Item.Title, o.target)
		return r.Queue.Complete(ctx, j.ID, "already in library", o.target)
	default:
		sum.Imported++
		r.logf("✓ %s → %s", j.Ref(), o.target)
		return r.Queue.Complete(ctx, j.ID, "imported", o.target)
	}
}

// finishAlbums ingests albums whose tracks have all finished downloading.
func (r *Runner) finishAlbums(ctx context.Context, sum *Summary) error {
	albums, err := r.Queue.Running(ctx, source.KindAlbum)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	for _, a := range albums {
		children, err := r.Queue.Children(ctx, a.ID)
		if err != nil {
			return err
		}
		var staged []string
		byFile := map[string]queue.Job{}
		waiting, failed := false, 0
		for _, c := range children {
			switch {
			case c.Status == queue.Pending || c.Status == queue.Running:
				waiting = true
			case c.Status == queue.Failed:
				failed++
			case c.Status == queue.Done && c.Note == "downloaded":
				f := r.abs(c.File)
				staged = append(staged, f)
				byFile[f] = c
			}
		}
		if waiting || len(children) == 0 {
			continue
		}
		before := *sum
		for f, o := range r.ingest(ctx, staged) {
			if err := r.finishTrack(ctx, byFile[f], o, sum, &mu); err != nil {
				return err
			}
		}
		for _, c := range children {
			r.warnCleanup(cleanup.RemoveAll(r.abs(r.jobDir(c))))
		}
		note := fmt.Sprintf("%d imported, %d already in library, %d failed",
			sum.Imported-before.Imported, sum.Skipped-before.Skipped, failed+sum.Failed-before.Failed)
		if err := r.Queue.Complete(ctx, a.ID, note, ""); err != nil {
			return err
		}
		r.logf("■ %s %s — %s: %s", a.Ref(), a.Item.Artist, a.Item.Title, note)
	}
	return nil
}
