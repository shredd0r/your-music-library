package library

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"your-music-library/internal/metadata"
	"your-music-library/internal/tags"
)

// IdentifyItem is one file's planned tag changes.
type IdentifyItem struct {
	Path    string
	Changes []tags.Change
	track   int // index into the release's tracks
}

// AlbumMatch is a group of files confidently matched to one release.
type AlbumMatch struct {
	Folder    string
	Release   metadata.Release
	Score     float64
	Items     []IdentifyItem
	Unmatched []string // files of the group that matched no track
}

// ReviewDraft is a group whose best match was not confident enough.
type ReviewDraft struct {
	Folder     string
	Paths      []string
	Query      metadata.Query
	Candidates []metadata.Match
}

// IdentifyPlan is the preview produced by PlanIdentify.
type IdentifyPlan struct {
	Matches  []AlbumMatch
	Reviews  []ReviewDraft
	Skipped  int // files already identified (use Force to redo)
	Untitled []string
	Failed   []FileError
}

// IdentifyOptions select what PlanIdentify looks at.
type IdentifyOptions struct {
	// Prefixes limit the plan to files under these relative paths.
	Prefixes []string
	// Force re-identifies files that already carry a release ID.
	Force bool
	// Progress, if set, is called before each group is looked up.
	Progress func(done, total int, folder string)
}

type identifyGroup struct {
	query   metadata.Query
	entries []Entry
}

func identified(e Entry) bool { return e.IDs.DiscogsReleaseID != "" || e.IDs.MBReleaseID != "" }

func underAny(p string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, pre := range prefixes {
		pre = strings.Trim(pre, "/")
		if pre == "" || p == pre || strings.HasPrefix(p, pre+"/") {
			return true
		}
	}
	return false
}

// PlanIdentify looks up releases for groups of indexed files (one group per
// album; tracks without an album are looked up alone). It changes no files.
func (l *Library) PlanIdentify(ctx context.Context, id *metadata.Identifier, opts IdentifyOptions) (IdentifyPlan, error) {
	var plan IdentifyPlan
	es, err := l.Entries(ctx)
	if err != nil {
		return plan, err
	}
	var todo []Entry
	for _, e := range es {
		if !underAny(e.Path, opts.Prefixes) {
			continue
		}
		if identified(e) && !opts.Force {
			plan.Skipped++
			continue
		}
		todo = append(todo, e)
	}
	plan.Matches, plan.Reviews, plan.Untitled, plan.Failed, err = identifyEntries(ctx, id, todo, opts.Progress)
	return plan, err
}

// identifyEntries groups entries by album and looks each group up. Entries
// only need tags and a Path used as key; files are not read, so this also
// works for files that are not in the library yet.
func identifyEntries(ctx context.Context, id *metadata.Identifier, es []Entry, progress func(done, total int, folder string)) (
	matches []AlbumMatch, reviews []ReviewDraft, untitled []string, failed []FileError, err error) {
	groups := map[string]*identifyGroup{}
	var keys []string
	for _, e := range es {
		if strings.TrimSpace(e.Title) == "" {
			untitled = append(untitled, e.Path)
			continue
		}
		album := e.Album
		key := pathKey(e.EffectiveAlbumArtist()) + "\x00" + pathKey(album)
		if album == "" {
			key += "\x00" + e.Path
		}
		g, ok := groups[key]
		if !ok {
			g = &identifyGroup{query: metadata.Query{Artist: e.EffectiveAlbumArtist(), Album: album}}
			groups[key] = g
			keys = append(keys, key)
		}
		g.entries = append(g.entries, e)
		g.query.Tracks = append(g.query.Tracks, metadata.QueryTrack{
			Title: e.Title, Number: e.TrackNumber, Disc: e.DiscNumber, ISRC: e.IDs.ISRC,
		})
	}
	sort.Strings(keys)

	for i, key := range keys {
		g := groups[key]
		folder := path.Dir(g.entries[0].Path)
		if progress != nil {
			progress(i, len(keys), folder)
		}
		res, err := id.Identify(ctx, g.query)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, nil, nil, ctx.Err()
			}
			for _, e := range g.entries {
				failed = append(failed, FileError{e.Path, err})
			}
			continue
		}
		if res.Best == nil {
			draft := ReviewDraft{Folder: folder, Query: g.query, Candidates: res.Candidates}
			for _, e := range g.entries {
				draft.Paths = append(draft.Paths, e.Path)
			}
			reviews = append(reviews, draft)
			continue
		}
		matches = append(matches, buildAlbumMatch(folder, g.entries, *res.Best))
	}
	return matches, reviews, untitled, failed, nil
}

func buildAlbumMatch(folder string, entries []Entry, m metadata.Match) AlbumMatch {
	am := AlbumMatch{Folder: folder, Release: m.Release, Score: m.Score}
	for i, e := range entries {
		if i >= len(m.Assign) || m.Assign[i] < 0 {
			am.Unmatched = append(am.Unmatched, e.Path)
			continue
		}
		// Entries carry no pictures, so Format has no cover to fail on.
		want, _ := tags.Format(ApplyRelease(e.Track, m.Release, m.Assign[i]))
		am.Items = append(am.Items, IdentifyItem{Path: e.Path, Changes: tags.Diff(e.Track, want), track: m.Assign[i]})
	}
	return am
}

// ApplyRelease returns cur with every release-derived field replaced by
// the values of rel's track ti (whole-record: nothing is mixed in from
// another release, except the genre and cover fill done by
// metadata.Identifier). Pictures are not touched here.
func ApplyRelease(cur tags.Track, rel metadata.Release, ti int) tags.Track {
	rt := rel.Tracks[ti]
	out := cur
	out.Title = rt.Title
	out.Artist = rt.Artist
	out.Artists = nil
	if len(rt.Artists) > 1 {
		out.Artists = append([]string(nil), rt.Artists...)
	}
	out.Date = rel.Date
	out.Genre = rel.Genre
	out.ReleaseType = rel.ReleaseType
	out.IDs.ISRC = rt.ISRC
	out.IDs.MBRecordingID, out.IDs.MBReleaseID, out.IDs.DiscogsReleaseID = "", "", ""
	switch rel.Provider {
	case "musicbrainz":
		out.IDs.MBRecordingID = rt.MBRecordingID
		out.IDs.MBReleaseID = rel.ID
	case "discogs":
		out.IDs.DiscogsReleaseID = rel.ID
	}
	out.Album = rel.Title
	out.AlbumArtist = rel.Artist
	out.TrackNumber, out.DiscNumber = rt.Number, rt.Disc
	out.TrackTotal, out.DiscTotal = 0, 0
	return out
}

// ImageSource fetches cover images (metadata.ImageFetcher in production).
type ImageSource interface {
	Fetch(ctx context.Context, url string) ([]byte, string, error)
}

// IdentifyReport summarizes ApplyIdentify.
type IdentifyReport struct {
	Changed  int
	Reviews  int
	Failed   []FileError
	Warnings []string // e.g. cover download failures; tags were still written
}

// ApplyIdentify writes matched releases (with covers, in the fixed tag
// format) and records review drafts in the Needs Review list.
func (l *Library) ApplyIdentify(ctx context.Context, plan IdentifyPlan, images ImageSource) (IdentifyReport, error) {
	var r IdentifyReport
	tagged := map[string]bool{}
	for _, am := range plan.Matches {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		front := l.fetchCover(ctx, am.Release, images, &r)
		for _, it := range am.Items {
			if err := l.writeRelease(ctx, it.Path, am.Release, it.track, front, path.Dir(it.Path)); err != nil {
				r.Failed = append(r.Failed, FileError{it.Path, err})
				continue
			}
			tagged[it.Path] = true
			r.Changed++
		}
	}
	if err := l.closeReviewsFor(ctx, tagged); err != nil {
		return r, err
	}
	for _, d := range plan.Reviews {
		if err := l.addReview(ctx, d); err != nil {
			return r, err
		}
		r.Reviews++
	}
	return r, nil
}

// fetchCover downloads rel's front cover. Back covers are not used.
func (l *Library) fetchCover(ctx context.Context, rel metadata.Release, images ImageSource, r *IdentifyReport) *tags.Picture {
	if images == nil || rel.FrontURL == "" {
		return nil
	}
	data, mime, err := images.Fetch(ctx, rel.FrontURL)
	if err != nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s: front cover: %v", rel.Title, err))
		return nil
	}
	return &tags.Picture{MIME: mime, Data: data}
}

func (l *Library) writeRelease(ctx context.Context, rel string, release metadata.Release, ti int, front *tags.Picture, imageDir string) error {
	abs := l.abs(rel)
	cur, err := tags.Read(abs)
	if err != nil {
		return err
	}
	want := ApplyRelease(cur, release, ti)
	if front != nil {
		want.Cover = front
	}
	return l.writeFormatted(ctx, rel, want, imageDir)
}

// writeFormatted saves t's full-size cover as cover.jpg in imageDir (if
// missing), brings t to the fixed tag format and writes it to rel.
func (l *Library) writeFormatted(ctx context.Context, rel string, t tags.Track, imageDir string) error {
	if err := l.saveFolderCover(imageDir, t.Cover); err != nil {
		return err
	}
	t, err := tags.Format(t)
	if err != nil {
		return err
	}
	if err := tags.Write(l.abs(rel), t); err != nil {
		return err
	}
	return l.reindex(ctx, rel)
}

// saveFolderCover writes front as the full-size folder cover unless the
// folder already has one.
func (l *Library) saveFolderCover(dir string, front *tags.Picture) error {
	if front == nil || l.hasFolderImage(dir, "cover") {
		return nil
	}
	if err := os.MkdirAll(l.abs(dir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(l.abs(path.Join(dir, "cover"+imageExt(front.MIME))), front.Data, 0o644)
}
