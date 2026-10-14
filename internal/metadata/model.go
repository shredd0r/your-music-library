// Package metadata identifies releases with online providers (Discogs,
// MusicBrainz) and scores how well they match local files. A match is
// "whole record": every tag of a file comes from one release of one
// provider.
package metadata

import (
	"context"
	"strings"
	"time"
)

// Provider is one metadata source.
type Provider interface {
	Name() string
	// Search returns cheap release summaries for q, best first.
	Search(ctx context.Context, q Query) ([]Candidate, error)
	// Release fetches full details, including the track list.
	Release(ctx context.Context, id string) (Release, error)
}

// Query describes local files believed to belong to one release.
type Query struct {
	Artist string // album artist, or track artist
	Album  string // may be empty for a lone track
	Tracks []QueryTrack
}

// QueryTrack is one local file.
type QueryTrack struct {
	Title    string
	Number   int
	Disc     int
	Duration time.Duration // 0 if unknown
	ISRC     string
}

// Candidate is a search hit before details are fetched.
type Candidate struct {
	ID          string
	Title       string
	Artist      string
	Year        int
	TrackCount  int // 0 if unknown
	ReleaseType string
	Compilation bool
}

// Release is a full release from one provider.
type Release struct {
	Provider string
	ID       string
	URL      string // human-readable page, for review

	Title         string
	Artist        string   // credit as printed, e.g. "Simon & Garfunkel"
	Artists       []string // individual artists
	Date          string   // YYYY[-MM[-DD]]; the original release date when known
	ReleaseType   string   // album, single, ep, compilation (MusicBrainz vocabulary)
	Genre         string
	Label         string
	CatalogNumber string
	Barcode       string
	Country       string
	Format        string // CD, Vinyl, Digital Media, ...

	Tracks   []Track
	FrontURL string
	BackURL  string

	MasterID string // Discogs master release, used to find a full date
}

// DateRefiner is implemented by providers that can look further for a
// full yyyy-mm-dd date when a release only has a year or year-month.
type DateRefiner interface {
	// RefineDate returns a more precise date for rel, or "" if none.
	RefineDate(ctx context.Context, rel Release) (string, error)
}

// fullDate reports whether d is a complete yyyy-mm-dd date.
func fullDate(d string) bool { return len(d) == len("2006-01-02") }

// moreSpecific reports whether cand refines cur ("1987" -> "1987-07-27").
// A date from another year is not a refinement.
func moreSpecific(cur, cand string) bool {
	return len(cand) > len(cur) && strings.HasPrefix(cand, cur)
}

// Track is one track of a Release.
type Track struct {
	Disc, Number  int
	Title         string
	Artist        string
	Artists       []string
	Duration      time.Duration
	ISRC          string
	MBRecordingID string
}

// DiscTotal is the number of discs.
func (r Release) DiscTotal() int {
	n := 0
	for _, t := range r.Tracks {
		n = max(n, t.Disc)
	}
	return n
}

// TrackTotal is the number of tracks on disc.
func (r Release) TrackTotal(disc int) int {
	n := 0
	for _, t := range r.Tracks {
		if t.Disc == disc {
			n++
		}
	}
	return n
}
