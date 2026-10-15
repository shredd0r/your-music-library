package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"your-music-library/internal/cleanup"
	"your-music-library/internal/config"
	"your-music-library/internal/metadata"
	"your-music-library/internal/pathtmpl"
	"your-music-library/internal/tags"
)

// StagingDir is the hidden folder inside the library where files are
// prepared before they get their final name. Scan skips it.
const StagingDir = ".ymlib-incoming"

// IngestItem is one file to bring into the library.
type IngestItem struct {
	Source  string // absolute path of the file to import
	Display string // shown in previews (relative to the import folder)
	Target  string // final path relative to the library
	Tags    tags.Track
	Dup     DupStatus // OtherEdition is imported; InLibrary is skipped unless forced
	DupPath string
	match   int // index into IngestPlan.Matches, or -1
	track   int // release track index when matched
}

// IngestPlan is the preview produced by PlanIngest.
type IngestPlan struct {
	Items       []IngestItem
	Duplicates  []IngestItem // already in the library; not imported
	Matches     []AlbumMatch
	Reviews     []ReviewDraft // imported with their own tags, then listed for review
	Conflicts   []Conflict
	Unsupported []string
	Failed      []FileError
}

// IngestOptions control PlanIngest/ApplyIngest.
type IngestOptions struct {
	// Base is the folder Display paths are relative to.
	Base string
	// Force imports files that are already in the library.
	Force bool
	// Move deletes source files after a successful import.
	Move bool
	// Progress is called before each album lookup.
	Progress func(done, total int, folder string)
}

// CollectAudio lists audio files under dir, skipping hidden entries.
func CollectAudio(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != dir && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && tags.IsAudio(p) {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// PlanIngest reads the files, skips duplicates, identifies albums (when id
// is not nil) and computes target paths. It changes nothing.
func (l *Library) PlanIngest(ctx context.Context, files []string, id *metadata.Identifier, opts IngestOptions) (IngestPlan, error) {
	var plan IngestPlan
	dups, err := l.DupIndex(ctx)
	if err != nil {
		return plan, err
	}
	var entries []Entry
	byDisplay := map[string]*IngestItem{}
	var items []*IngestItem
	for _, f := range files {
		display := f
		if opts.Base != "" {
			if rel, err := filepath.Rel(opts.Base, f); err == nil {
				display = filepath.ToSlash(rel)
			}
		}
		if !tags.Supported(f) {
			plan.Unsupported = append(plan.Unsupported, display)
			continue
		}
		t, err := tags.Read(f)
		if err != nil {
			plan.Failed = append(plan.Failed, FileError{display, err})
			continue
		}
		t.Cover, t.Back = nil, nil // pictures are read again when writing
		it := &IngestItem{Source: f, Display: display, Tags: t, match: -1}
		it.Dup, it.DupPath = dups.Check(dupQuery(t))
		if it.Dup == InLibrary && !opts.Force {
			plan.Duplicates = append(plan.Duplicates, *it)
			continue
		}
		items = append(items, it)
		byDisplay[display] = it
		entries = append(entries, Entry{Path: display, Track: t})
	}

	if id != nil && len(entries) > 0 {
		var untitled []string
		var failed []FileError
		plan.Matches, plan.Reviews, untitled, failed, err = identifyEntries(ctx, id, entries, opts.Progress)
		if err != nil {
			return plan, err
		}
		_ = untitled // imported with their tags; organize names them "Untitled"
		plan.Failed = append(plan.Failed, failed...)
		for mi, am := range plan.Matches {
			for _, mit := range am.Items {
				it := byDisplay[mit.Path]
				it.match, it.track = mi, mit.track
				it.Tags = ApplyRelease(it.Tags, am.Release, mit.track)
			}
		}
	}
	failedPaths := map[string]bool{}
	for _, f := range plan.Failed {
		failedPaths[f.Path] = true
	}

	targets, err := l.ingestTargets(ctx, items)
	if err != nil {
		return plan, err
	}
	claimed := map[string]string{}
	for i, it := range items {
		if failedPaths[it.Display] {
			continue
		}
		it.Target = targets[i]
		if other, ok := claimed[pathKey(it.Target)]; ok {
			plan.Conflicts = append(plan.Conflicts, Conflict{it.Display, it.Target, "same target as " + other})
			continue
		}
		if exists(l.abs(it.Target)) {
			plan.Conflicts = append(plan.Conflicts, Conflict{it.Display, it.Target, "target file already exists"})
			continue
		}
		claimed[pathKey(it.Target)] = it.Display
		plan.Items = append(plan.Items, *it)
	}
	return plan, nil
}

func dupQuery(t tags.Track) DupQuery {
	return DupQuery{
		Source: t.IDs.Source, SourceTrackID: t.IDs.SourceTrackID, ISRC: t.IDs.ISRC, MBRecordingID: t.IDs.MBRecordingID,
		Artist: t.Artist, AlbumArtist: t.AlbumArtist, Album: t.Album, Title: t.Title,
	}
}

// ingestTargets renders template paths; an album is multi-disc if any of
// its tracks, here or already in the library, says so.
func (l *Library) ingestTargets(ctx context.Context, items []*IngestItem) ([]string, error) {
	es, err := l.Entries(ctx)
	if err != nil {
		return nil, err
	}
	discTotal := map[string]int{}
	for _, e := range es {
		k := albumKey(e)
		discTotal[k] = max(discTotal[k], e.DiscTotal, e.DiscNumber)
	}
	for _, it := range items {
		k := albumKey(Entry{Track: it.Tags})
		discTotal[k] = max(discTotal[k], it.Tags.DiscTotal, it.Tags.DiscNumber)
	}
	out := make([]string, len(items))
	for i, it := range items {
		t := it.Tags
		to, err := l.template.Render(pathtmpl.Values{
			AlbumArtist: t.AlbumArtist, Artist: t.Artist, Album: t.Album, Title: t.Title,
			Genre: t.Genre, Date: t.Date, Track: t.TrackNumber, Disc: t.DiscNumber,
			DiscTotal: discTotal[albumKey(Entry{Track: t})],
			Ext:       strings.ToLower(strings.TrimPrefix(filepath.Ext(it.Source), ".")),
		})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", it.Display, err)
		}
		out[i] = to
	}
	return out, nil
}

// IngestReport summarizes ApplyIngest.
type IngestReport struct {
	Imported []string // library paths
	Reviews  int
	Failed   []FileError
	Warnings []string
}

// ApplyIngest copies each file into a staging folder inside the library,
// writes its tags (release, covers, preset) and only then moves it to its
// target, so nothing half-done ever appears in the library. Files that
// fail stay where they were; with Move, sources are deleted only after
// their import succeeded.
func (l *Library) ApplyIngest(ctx context.Context, plan IngestPlan, preset config.Preset, images ImageSource, opts IngestOptions) (r IngestReport, err error) {
	stage := path.Join(StagingDir, "import-"+strconv.FormatInt(l.now().UnixNano(), 36))
	if err := os.MkdirAll(l.abs(stage), 0o755); err != nil {
		return r, err
	}
	defer func() {
		// removed only when empty: failed files stay for inspection
		err = errors.Join(err, cleanup.RemoveIfEmpty(l.abs(stage)), cleanup.RemoveIfEmpty(l.abs(StagingDir)))
	}()

	covers := map[int][2]*tags.Picture{}
	final := map[string]string{} // display -> library path
	for i, it := range plan.Items {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		staged := path.Join(stage, strconv.Itoa(i)+strings.ToLower(filepath.Ext(it.Source)))
		if err := l.ingestOne(ctx, it, staged, plan, preset, images, covers, &r); err != nil {
			err = errors.Join(err, cleanup.Remove(l.abs(staged)))
			if _, derr := l.db.ExecContext(ctx, "DELETE FROM tracks WHERE path = ?", staged); derr != nil {
				err = errors.Join(err, derr)
			}
			r.Failed = append(r.Failed, FileError{it.Display, err})
			continue
		}
		final[it.Display] = it.Target
		r.Imported = append(r.Imported, it.Target)
		if opts.Move && !strings.HasPrefix(it.Source, l.abs(StagingDir)+string(filepath.Separator)) {
			if err := os.Remove(it.Source); err != nil {
				r.Warnings = append(r.Warnings, fmt.Sprintf("%s imported but not removed: %v", it.Display, err))
			}
		}
	}
	for _, d := range plan.Reviews {
		var paths []string
		for _, p := range d.Paths {
			if to, ok := final[p]; ok {
				paths = append(paths, to)
			}
		}
		if len(paths) == 0 {
			continue
		}
		d.Paths, d.Folder = paths, path.Dir(paths[0])
		if err := l.addReview(ctx, d); err != nil {
			return r, err
		}
		r.Reviews++
	}
	return r, nil
}

func (l *Library) ingestOne(ctx context.Context, it IngestItem, staged string, plan IngestPlan, preset config.Preset,
	images ImageSource, covers map[int][2]*tags.Picture, r *IngestReport) error {
	if err := placeFile(it.Source, l.abs(staged), strings.HasPrefix(it.Source, l.abs(StagingDir)+string(filepath.Separator))); err != nil {
		return err
	}
	imageDir := path.Dir(it.Target)
	if it.match >= 0 {
		am := plan.Matches[it.match]
		c, ok := covers[it.match]
		if !ok {
			var rep IdentifyReport
			c[0], c[1] = l.fetchCovers(ctx, am.Release, images, &rep)
			covers[it.match] = c
			r.Warnings = append(r.Warnings, rep.Warnings...)
		}
		if err := l.writeRelease(ctx, staged, am.Release, it.track, c[0], c[1], preset, imageDir); err != nil {
			return err
		}
	} else {
		cur, err := tags.Read(l.abs(staged))
		if err != nil {
			return err
		}
		if err := l.writeWithPreset(ctx, staged, cur, preset, imageDir); err != nil {
			return err
		}
	}
	if exists(l.abs(it.Target)) {
		return fmt.Errorf("target %s appeared during import", it.Target)
	}
	if err := l.rename(staged, it.Target); err != nil {
		return err
	}
	_, err := l.db.ExecContext(ctx, "UPDATE tracks SET path = ? WHERE path = ?", it.Target, staged)
	return err
}

// placeFile moves (inside the library) or copies src to dst.
func placeFile(src, dst string, move bool) (err error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if move {
		return os.Rename(src, dst)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer cleanup.Close(&err, in)
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	if err := errors.Join(cerr, out.Close()); err != nil {
		return errors.Join(err, cleanup.Remove(dst))
	}
	now := time.Now()
	return os.Chtimes(dst, now, now)
}
