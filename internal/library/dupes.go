package library

import (
	"context"
	"strings"

	"your-music-library/internal/metadata"
)

// DupStatus says whether something is already in the library.
type DupStatus int

const (
	NotInLibrary DupStatus = iota
	// InLibrary: the same recording on the same album is present.
	InLibrary
	// OtherEdition: the same recording is present on another album
	// (remaster, deluxe, compilation, single).
	OtherEdition
)

// Symbol is the flag shown next to search results.
func (s DupStatus) Symbol() string {
	switch s {
	case InLibrary:
		return "✓"
	case OtherEdition:
		return "≈"
	}
	return " "
}

// DupQuery describes a track to look for.
type DupQuery struct {
	Source, SourceTrackID string
	ISRC, MBRecordingID   string
	Artist, AlbumArtist   string
	Album, Title          string
}

// DupIndex answers duplicate questions for a snapshot of the index.
type DupIndex struct {
	bySource map[string]Entry
	byISRC   map[string][]Entry
	byMBRec  map[string][]Entry
	byTitle  map[string][]Entry // artist + title
}

// DupIndex loads the index for duplicate checks.
func (l *Library) DupIndex(ctx context.Context) (*DupIndex, error) {
	es, err := l.Entries(ctx)
	if err != nil {
		return nil, err
	}
	d := &DupIndex{bySource: map[string]Entry{}, byISRC: map[string][]Entry{}, byMBRec: map[string][]Entry{}, byTitle: map[string][]Entry{}}
	for _, e := range es {
		d.Add(e)
	}
	return d, nil
}

// Add includes e in later checks (e.g. a file imported a moment ago).
func (d *DupIndex) Add(e Entry) {
	if e.IDs.Source != "" && e.IDs.SourceTrackID != "" {
		d.bySource[e.IDs.Source+"\x00"+e.IDs.SourceTrackID] = e
	}
	if isrc := strings.ToUpper(e.IDs.ISRC); isrc != "" {
		d.byISRC[isrc] = append(d.byISRC[isrc], e)
	}
	if e.IDs.MBRecordingID != "" {
		d.byMBRec[e.IDs.MBRecordingID] = append(d.byMBRec[e.IDs.MBRecordingID], e)
	}
	for _, artist := range uniqueNonEmpty(e.Artist, e.EffectiveAlbumArtist()) {
		k := titleKey(artist, e.Title)
		d.byTitle[k] = append(d.byTitle[k], e)
	}
}

func titleKey(artist, title string) string {
	return metadata.Normalize(artist) + "\x00" + metadata.NormalizeTitle(title)
}

func uniqueNonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x != "" && (len(out) == 0 || out[len(out)-1] != x) {
			out = append(out, x)
		}
	}
	return out
}

// sameAlbum compares album names exactly (after normalization), so a
// remaster or deluxe edition counts as another edition. Grouped singles
// are compared by their original album too.
func sameAlbum(q DupQuery, e Entry) bool {
	want := metadata.Normalize(q.Album)
	if want == "" {
		return true // nothing to tell editions apart
	}
	return want == metadata.Normalize(e.Album) || (e.OriginalAlbum != "" && want == metadata.Normalize(e.OriginalAlbum))
}

// Check returns the status of q and the matching file, if any.
func (d *DupIndex) Check(q DupQuery) (DupStatus, string) {
	if q.Source != "" && q.SourceTrackID != "" {
		if e, ok := d.bySource[q.Source+"\x00"+q.SourceTrackID]; ok {
			return InLibrary, e.Path
		}
	}
	var other string
	judge := func(es []Entry) (DupStatus, string) {
		for _, e := range es {
			if sameAlbum(q, e) {
				return InLibrary, e.Path
			}
			if other == "" {
				other = e.Path
			}
		}
		return NotInLibrary, ""
	}
	if isrc := strings.ToUpper(q.ISRC); isrc != "" {
		if s, p := judge(d.byISRC[isrc]); s == InLibrary {
			return s, p
		}
	}
	if q.MBRecordingID != "" {
		if s, p := judge(d.byMBRec[q.MBRecordingID]); s == InLibrary {
			return s, p
		}
	}
	for _, artist := range uniqueNonEmpty(q.Artist, q.AlbumArtist) {
		if s, p := judge(d.byTitle[titleKey(artist, q.Title)]); s == InLibrary {
			return s, p
		}
	}
	if other != "" {
		return OtherEdition, other
	}
	return NotInLibrary, ""
}
