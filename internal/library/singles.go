package library

import (
	"context"
	"sort"
	"strings"

	"your-music-library/internal/config"
	"your-music-library/internal/tags"
)

// SinglesItem is one file whose tags change when switching singles mode.
type SinglesItem struct {
	Path    string
	Changes []tags.Change
	target  tags.Track // fields to write; pictures are taken from the file
}

// GroupedAlbumName is the album that collects an artist's singles.
func GroupedAlbumName(artist string) string { return artist + " Singles" }

// isSingle decides whether an indexed track is a single release.
// Releases tagged by a metadata provider carry RELEASETYPE; untagged files
// fall back to "the only track of an album named like the track".
func isSingle(e Entry, albumSize int) bool {
	if e.OriginalAlbum != "" || strings.EqualFold(e.ReleaseType, "single") {
		return true
	}
	return e.ReleaseType == "" && albumSize == 1 && sameTitle(e.Album, e.Title)
}

func sameTitle(album, title string) bool {
	a := strings.ToLower(strings.TrimSpace(album))
	for _, suffix := range []string{" - single", " (single)", " [single]"} {
		a = strings.TrimSuffix(a, suffix)
	}
	return a != "" && a == strings.ToLower(strings.TrimSpace(title))
}

// PlanSingles lists tag changes needed to store singles in mode. Grouped
// mode numbers each artist's singles by release date; as-album mode
// restores each single's own album and track number. Run organize
// afterwards to move the files.
func (l *Library) PlanSingles(ctx context.Context, mode config.SinglesMode) ([]SinglesItem, error) {
	es, err := l.Entries(ctx)
	if err != nil {
		return nil, err
	}
	albumSize := map[string]int{}
	for _, e := range es {
		albumSize[albumKey(e)]++
	}
	byArtist := map[string][]Entry{}
	var artists []string
	for _, e := range es {
		if !isSingle(e, albumSize[albumKey(e)]) {
			continue
		}
		a := e.EffectiveAlbumArtist()
		if _, ok := byArtist[a]; !ok {
			artists = append(artists, a)
		}
		byArtist[a] = append(byArtist[a], e)
	}
	sort.Strings(artists)

	var items []SinglesItem
	for _, artist := range artists {
		singles := byArtist[artist]
		sort.SliceStable(singles, func(i, j int) bool {
			di, dj := singles[i].Date, singles[j].Date
			if (di == "") != (dj == "") {
				return dj == "" // undated singles go last
			}
			if di != dj {
				return di < dj
			}
			return strings.ToLower(singles[i].Title) < strings.ToLower(singles[j].Title)
		})
		for i, e := range singles {
			want := e.Track
			switch mode {
			case config.SinglesGrouped:
				if want.OriginalAlbum == "" {
					want.OriginalAlbum = e.Album
					want.OriginalTrack = e.TrackNumber
				}
				want.Album = GroupedAlbumName(artist)
				want.AlbumArtist = artist
				want.TrackNumber, want.TrackTotal = i+1, len(singles)
				want.DiscNumber, want.DiscTotal = 0, 0
				want.ReleaseType = "single"
			case config.SinglesAsAlbum:
				if e.OriginalAlbum == "" {
					continue
				}
				want.Album = e.OriginalAlbum
				want.TrackNumber = max(e.OriginalTrack, 1)
				want.TrackTotal = 0
				want.OriginalAlbum, want.OriginalTrack = "", 0
			}
			changes := tags.Diff(e.Track, want)
			if len(changes) == 0 && want.OriginalAlbum == e.OriginalAlbum && want.ReleaseType == e.ReleaseType {
				continue
			}
			items = append(items, SinglesItem{Path: e.Path, Changes: changes, target: want})
		}
	}
	return items, nil
}

// ApplySingles writes the planned tags. Pictures and identity tags in the
// files are kept.
func (l *Library) ApplySingles(ctx context.Context, items []SinglesItem) (ApplyReport, error) {
	var r ApplyReport
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		abs := l.abs(it.Path)
		cur, err := tags.Read(abs)
		if err != nil {
			r.Failed = append(r.Failed, FileError{it.Path, err})
			continue
		}
		cur.Album, cur.AlbumArtist = it.target.Album, it.target.AlbumArtist
		cur.TrackNumber, cur.TrackTotal = it.target.TrackNumber, it.target.TrackTotal
		cur.DiscNumber, cur.DiscTotal = it.target.DiscNumber, it.target.DiscTotal
		cur.ReleaseType = it.target.ReleaseType
		cur.OriginalAlbum, cur.OriginalTrack = it.target.OriginalAlbum, it.target.OriginalTrack
		if err := tags.Write(abs, cur); err != nil {
			r.Failed = append(r.Failed, FileError{it.Path, err})
			continue
		}
		if err := l.reindex(ctx, it.Path); err != nil {
			return r, err
		}
		r.Changed++
	}
	return r, nil
}
