package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/text/unicode/norm"

	"your-music-library/internal/cleanup"
	"your-music-library/internal/pathtmpl"
)

// Move is a planned rename, with paths relative to the library root.
type Move struct{ From, To string }

// Conflict is a move that was not planned, and why.
type Conflict struct {
	From, To, Reason string
}

// OrganizePlan is the preview produced by PlanOrganize.
type OrganizePlan struct {
	Moves     []Move // audio files
	Images    []Move // cover.jpg etc. that follow their album folder
	Conflicts []Conflict
	Unchanged int
}

// folderImages are album-folder images that move with their tracks.
var folderImages = []string{"cover.jpg", "cover.png", "back.jpg", "back.png", "folder.jpg"}

// pathKey compares paths the way case-insensitive, normalizing file
// systems (macOS, FAT, exFAT) do, so collisions are caught everywhere.
func pathKey(p string) string { return strings.ToLower(norm.NFC.String(p)) }

func albumKey(e Entry) string {
	return pathKey(e.EffectiveAlbumArtist()) + "\x00" + pathKey(e.Album)
}

// PlanOrganize computes where every indexed file belongs according to the
// path template. It changes nothing; run Scan first so the index is current.
func (l *Library) PlanOrganize(ctx context.Context) (OrganizePlan, error) {
	var plan OrganizePlan
	es, err := l.Entries(ctx)
	if err != nil {
		return plan, err
	}

	// A track counts as multi-disc if its album does, even when its own
	// DISCTOTAL tag is missing.
	discTotal := map[string]int{}
	for _, e := range es {
		k := albumKey(e)
		discTotal[k] = max(discTotal[k], e.DiscTotal, e.DiscNumber)
	}

	targets := make([]string, len(es))
	claimed := map[string]string{} // pathKey(target) -> source
	for i, e := range es {
		to, err := l.template.Render(pathtmpl.Values{
			AlbumArtist: e.AlbumArtist, Artist: e.Artist, Album: e.Album, Title: e.Title,
			Genre: e.Genre, Date: e.Date, Track: e.TrackNumber, Disc: e.DiscNumber,
			DiscTotal: discTotal[albumKey(e)], Ext: strings.ToLower(strings.TrimPrefix(path.Ext(e.Path), ".")),
		})
		if err != nil {
			return plan, fmt.Errorf("%s: %w", e.Path, err)
		}
		targets[i] = to
		if to == e.Path {
			plan.Unchanged++
			claimed[pathKey(to)] = e.Path
		}
	}

	moved := map[string]int{}               // source dir -> moved audio count
	movedTo := map[string]map[string]bool{} // source dir -> target dirs
	total := map[string]int{}               // source dir -> audio count
	for i, e := range es {
		total[path.Dir(e.Path)]++
		to := targets[i]
		if to == e.Path {
			continue
		}
		if other, ok := claimed[pathKey(to)]; ok {
			plan.Conflicts = append(plan.Conflicts, Conflict{e.Path, to, "same target as " + other})
			continue
		}
		if l.occupied(e.Path, to) {
			plan.Conflicts = append(plan.Conflicts, Conflict{e.Path, to, "target file already exists"})
			continue
		}
		claimed[pathKey(to)] = e.Path
		plan.Moves = append(plan.Moves, Move{e.Path, to})
		dir := path.Dir(e.Path)
		moved[dir]++
		if movedTo[dir] == nil {
			movedTo[dir] = map[string]bool{}
		}
		movedTo[dir][path.Dir(to)] = true
	}

	// Folder images follow only when the whole folder moves to one place.
	dirs := make([]string, 0, len(movedTo))
	for d := range movedTo {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if moved[dir] != total[dir] || len(movedTo[dir]) != 1 {
			continue
		}
		var target string
		for t := range movedTo[dir] {
			target = t
		}
		for _, name := range folderImages {
			from := path.Join(dir, name)
			if !exists(l.abs(from)) {
				continue
			}
			to := path.Join(target, name)
			if other, ok := claimed[pathKey(to)]; ok {
				// several folders merge into one (e.g. grouped singles)
				plan.Conflicts = append(plan.Conflicts, Conflict{from, to, "same target as " + other})
				continue
			}
			if l.occupied(from, to) {
				plan.Conflicts = append(plan.Conflicts, Conflict{from, to, "target file already exists"})
				continue
			}
			claimed[pathKey(to)] = from
			plan.Images = append(plan.Images, Move{from, to})
		}
	}
	return plan, nil
}

// occupied reports whether to exists and is not the same file as from
// (a case-only rename on a case-insensitive file system is allowed).
func (l *Library) occupied(from, to string) bool {
	ti, err := os.Stat(l.abs(to))
	if err != nil {
		return false
	}
	fi, err := os.Stat(l.abs(from))
	return err != nil || !os.SameFile(fi, ti)
}

// ApplyReport summarizes ApplyOrganize.
type ApplyReport struct {
	Changed int
	Failed  []FileError
}

// ApplyOrganize performs a plan from PlanOrganize. Each move is checked
// again before it happens; a file is never overwritten.
func (l *Library) ApplyOrganize(ctx context.Context, plan OrganizePlan) (ApplyReport, error) {
	var r ApplyReport
	// Audio moves come first; only they have index rows.
	moves := append(append([]Move{}, plan.Moves...), plan.Images...)
	sourceDirs := map[string]bool{}
	moved := map[string]string{} // original audio path -> new path

	for i := range moves {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		m := moves[i]
		if err := l.fixDirCase(ctx, path.Dir(m.To), moves[i:]); err != nil {
			return r, err
		}
		m = moves[i] // fixDirCase may have rewritten From
		if l.occupied(m.From, m.To) {
			r.Failed = append(r.Failed, FileError{m.From, fmt.Errorf("target %s already exists", m.To)})
			continue
		}
		if err := l.rename(m.From, m.To); err != nil {
			r.Failed = append(r.Failed, FileError{m.From, err})
			continue
		}
		if i < len(plan.Moves) {
			if _, err := l.db.ExecContext(ctx, "UPDATE tracks SET path = ? WHERE path = ?", m.To, m.From); err != nil {
				return r, err
			}
			moved[plan.Moves[i].From] = m.To
		}
		sourceDirs[path.Dir(m.From)] = true
		r.Changed++
	}
	for dir := range sourceDirs {
		l.removeEmptyDirs(dir)
	}
	return r, l.followMoves(ctx, moved)
}

func (l *Library) rename(from, to string) error {
	src, dst := l.abs(from), l.abs(to)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if strings.EqualFold(from, to) {
		// Case-only rename: go through a temporary name so it also works
		// on case-insensitive file systems.
		tmp := src + ".ymlib-rename"
		if err := os.Rename(src, tmp); err != nil {
			return err
		}
		return os.Rename(tmp, dst)
	}
	return os.Rename(src, dst)
}

// fixDirCase renames existing folders whose name differs from dir only in
// letter case (possible only on case-insensitive file systems), and
// rewrites pending moves and index rows that pointed into them.
func (l *Library) fixDirCase(ctx context.Context, dir string, pending []Move) error {
	parts := strings.Split(dir, "/")
	for i := range parts {
		want := strings.Join(parts[:i+1], "/")
		parent := l.abs(path.Dir(want))
		entries, err := os.ReadDir(parent)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		var actual string
		for _, de := range entries {
			if de.Name() == parts[i] {
				actual = ""
				break
			}
			if de.IsDir() && pathKey(de.Name()) == pathKey(parts[i]) {
				actual = de.Name()
			}
		}
		if actual == "" {
			continue
		}
		have := path.Join(path.Dir(want), actual)
		if path.Dir(want) == "." {
			have = actual
		}
		if err := l.rename(have, want); err != nil {
			return err
		}
		for j := range pending {
			if strings.HasPrefix(pending[j].From, have+"/") {
				pending[j].From = want + pending[j].From[len(have):]
			}
		}
		// length() and substr() count characters, so pass strings, not Go
		// byte lengths, to stay correct for non-ASCII names.
		if _, err := l.db.ExecContext(ctx,
			"UPDATE tracks SET path = ? || substr(path, length(?) + 1) WHERE substr(path, 1, length(?)) = ?",
			want, have, have+"/", have+"/"); err != nil {
			return err
		}
	}
	return nil
}

// removeEmptyDirs deletes dir and its parents up to the root while they
// are empty (a lone .DS_Store counts as empty).
func (l *Library) removeEmptyDirs(dir string) {
	for dir != "." && dir != "/" && dir != "" {
		abs := l.abs(dir)
		entries, err := os.ReadDir(abs)
		if err != nil {
			return
		}
		if len(entries) == 1 && entries[0].Name() == ".DS_Store" {
			if cleanup.Remove(filepath.Join(abs, ".DS_Store")) != nil {
				return // folder stays; nothing else depends on it
			}
		} else if len(entries) > 0 {
			return
		}
		if os.Remove(abs) != nil {
			return
		}
		dir = path.Dir(dir)
	}
}
