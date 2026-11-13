package library

import (
	"bufio"
	"context"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"your-music-library/internal/tags"
)

// PlaylistsDir holds the user's playlists, relative to the library root.
const PlaylistsDir = "Playlists"

// TidyPlan brings the library to the fixed tag format and then to the
// path template: the Tidy library action.
type TidyPlan struct {
	Retag RetagPlan
	// Organize is computed from the tags as they will be after Retag, so
	// the preview shows the final file names.
	Organize OrganizePlan
	// Playlists names the playlists in PlaylistsDir that reference files
	// Organize moves; those entries would break.
	Playlists []string
}

// TidyReport summarizes ApplyTidy.
type TidyReport struct {
	Retag, Organize ApplyReport
}

// PlanTidy previews Tidy library. It changes nothing; run Scan first so
// the index is current.
func (l *Library) PlanTidy(ctx context.Context) (TidyPlan, error) {
	var p TidyPlan
	var err error
	if p.Retag, err = l.PlanRetag(ctx); err != nil {
		return p, err
	}
	es, err := l.Entries(ctx)
	if err != nil {
		return p, err
	}
	retagged := map[string]bool{}
	for _, it := range p.Retag.Items {
		retagged[it.Path] = true
	}
	for i, e := range es {
		if retagged[e.Path] {
			// Entries carry no pictures, so Format has no cover to fail on.
			es[i].Track, _ = tags.Format(e.Track)
		}
	}
	if p.Organize, err = l.planOrganize(es); err != nil {
		return p, err
	}
	p.Playlists, err = l.playlistsReferencing(p.Organize.Moves)
	return p, err
}

// ApplyTidy writes the tags, then moves the files.
func (l *Library) ApplyTidy(ctx context.Context, p TidyPlan) (TidyReport, error) {
	var r TidyReport
	var err error
	if r.Retag, err = l.ApplyRetag(ctx, p.Retag); err != nil {
		return r, err
	}
	r.Organize, err = l.ApplyOrganize(ctx, p.Organize)
	return r, err
}

// playlistsReferencing lists, sorted by name without extension, the
// .m3u/.m3u8 files in PlaylistsDir with an entry for the source of a move.
// Entries may be relative to the playlist or absolute.
func (l *Library) playlistsReferencing(moves []Move) ([]string, error) {
	if len(moves) == 0 {
		return nil, nil
	}
	moved := map[string]bool{}
	for _, m := range moves {
		moved[pathKey(m.From)] = true
	}
	dir := l.abs(PlaylistsDir)
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f.Name()))
		if f.IsDir() || (ext != ".m3u" && ext != ".m3u8") {
			continue
		}
		hit, err := l.playlistHits(filepath.Join(dir, f.Name()), moved)
		if err != nil {
			return nil, err
		}
		if hit {
			out = append(out, strings.TrimSuffix(f.Name(), filepath.Ext(f.Name())))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (l *Library) playlistHits(file string, moved map[string]bool) (hit bool, err error) {
	f, err := os.Open(file)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }() // read only; a close error loses nothing
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var rel string
		if filepath.IsAbs(line) {
			r, err := filepath.Rel(l.root, line)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(r)
		} else {
			rel = path.Join(PlaylistsDir, filepath.ToSlash(line))
		}
		if moved[pathKey(rel)] {
			return true, nil
		}
	}
	return false, sc.Err()
}
