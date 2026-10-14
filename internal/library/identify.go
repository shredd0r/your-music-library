package library

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	"your-music-library/internal/config"
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
	groups := map[string]*identifyGroup{}
	var keys []string
	for _, e := range es {
		if !underAny(e.Path, opts.Prefixes) {
			continue
		}
		if identified(e) && !opts.Force {
			plan.Skipped++
			continue
		}
		if strings.TrimSpace(e.Title) == "" {
			plan.Untitled = append(plan.Untitled, e.Path)
			continue
		}
		// grouped singles are looked up as their own release
		album := e.Album
		if e.OriginalAlbum != "" {
			album = e.OriginalAlbum
		}
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
		disc := e.DiscNumber
		number := e.TrackNumber
		if e.OriginalAlbum != "" {
			number, disc = e.OriginalTrack, 0
		}
		g.query.Tracks = append(g.query.Tracks, metadata.QueryTrack{
			Title: e.Title, Number: number, Disc: disc, ISRC: e.IDs.ISRC,
		})
	}
	sort.Strings(keys)

	for i, key := range keys {
		g := groups[key]
		folder := path.Dir(g.entries[0].Path)
		if opts.Progress != nil {
			opts.Progress(i, len(keys), folder)
		}
		res, err := id.Identify(ctx, g.query)
		if err != nil {
			if ctx.Err() != nil {
				return plan, ctx.Err()
			}
			for _, e := range g.entries {
				plan.Failed = append(plan.Failed, FileError{e.Path, err})
			}
			continue
		}
		if res.Best == nil {
			draft := ReviewDraft{Folder: folder, Query: g.query, Candidates: res.Candidates}
			for _, e := range g.entries {
				draft.Paths = append(draft.Paths, e.Path)
			}
			plan.Reviews = append(plan.Reviews, draft)
			continue
		}
		plan.Matches = append(plan.Matches, buildAlbumMatch(folder, g.entries, *res.Best))
	}
	return plan, nil
}

func buildAlbumMatch(folder string, entries []Entry, m metadata.Match) AlbumMatch {
	am := AlbumMatch{Folder: folder, Release: m.Release, Score: m.Score}
	for i, e := range entries {
		if i >= len(m.Assign) || m.Assign[i] < 0 {
			am.Unmatched = append(am.Unmatched, e.Path)
			continue
		}
		want := ApplyRelease(e.Track, m.Release, m.Assign[i])
		am.Items = append(am.Items, IdentifyItem{Path: e.Path, Changes: tags.Diff(e.Track, want), track: m.Assign[i]})
	}
	return am
}

// ApplyRelease returns cur with every release-derived field replaced by
// the values of rel's track ti (whole-record: nothing is mixed in from
// another release). Pictures are not touched here. For a grouped single
// the group's album and numbering are kept and the release goes into
// OriginalAlbum/OriginalTrack.
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
	if cur.OriginalAlbum != "" {
		out.OriginalAlbum, out.OriginalTrack = rel.Title, rt.Number
		return out
	}
	out.Album = rel.Title
	out.AlbumArtist = rel.Artist
	out.TrackNumber, out.TrackTotal = rt.Number, rel.TrackTotal(rt.Disc)
	out.DiscNumber, out.DiscTotal = rt.Disc, rel.DiscTotal()
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

// ApplyIdentify writes matched releases (with covers, shaped by preset)
// and records review drafts in the Needs Review list.
func (l *Library) ApplyIdentify(ctx context.Context, plan IdentifyPlan, preset config.Preset, images ImageSource) (IdentifyReport, error) {
	var r IdentifyReport
	tagged := map[string]bool{}
	for _, am := range plan.Matches {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		front, back := l.fetchCovers(ctx, am.Release, images, &r)
		for _, it := range am.Items {
			if err := l.writeRelease(ctx, it.Path, am.Release, it.track, front, back, preset); err != nil {
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

func (l *Library) fetchCovers(ctx context.Context, rel metadata.Release, images ImageSource, r *IdentifyReport) (front, back *tags.Picture) {
	if images == nil {
		return nil, nil
	}
	get := func(url, what string) *tags.Picture {
		if url == "" {
			return nil
		}
		data, mime, err := images.Fetch(ctx, url)
		if err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s: %s cover: %v", rel.Title, what, err))
			return nil
		}
		return &tags.Picture{MIME: mime, Data: data}
	}
	return get(rel.FrontURL, "front"), get(rel.BackURL, "back")
}

func (l *Library) writeRelease(ctx context.Context, rel string, release metadata.Release, ti int, front, back *tags.Picture, preset config.Preset) error {
	abs := l.abs(rel)
	cur, err := tags.Read(abs)
	if err != nil {
		return err
	}
	want := ApplyRelease(cur, release, ti)
	if front != nil {
		want.Cover = front
	}
	if back != nil {
		want.Back = back
	}
	// full-size images go to the album folder before the preset shrinks them
	dir := path.Dir(rel)
	for _, pic := range []struct {
		p    *tags.Picture
		base string
	}{{want.Cover, "cover"}, {want.Back, "back"}} {
		if pic.p != nil && !l.hasFolderImage(dir, pic.base) {
			if err := os.WriteFile(l.abs(path.Join(dir, pic.base+imageExt(pic.p.MIME))), pic.p.Data, 0o644); err != nil {
				return err
			}
		}
	}
	want, err = tags.ApplyPreset(want, preset)
	if err != nil {
		return err
	}
	if err := tags.Write(abs, want); err != nil {
		return err
	}
	return l.reindex(ctx, rel)
}
