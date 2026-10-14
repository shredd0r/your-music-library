// Package tags reads and writes audio file metadata. File tags are the
// library's source of truth; this package is the only place that touches
// tag formats.
package tags

import (
	"fmt"
	"strconv"
	"strings"
)

// Picture is an embedded image.
type Picture struct {
	MIME string
	Data []byte
}

// Identity holds IDs used to recognize a recording again ("already
// downloaded"). They are written regardless of the preset.
type Identity struct {
	Source           string // source plugin name
	SourceTrackID    string // ID within that source
	ISRC             string
	MBRecordingID    string
	MBReleaseID      string
	DiscogsReleaseID string
}

// Track is the metadata of one audio file. Zero values mean "absent".
type Track struct {
	Title       string
	Album       string
	Artist      string
	Artists     []string
	AlbumArtist string
	Date        string // YYYY, YYYY-MM or YYYY-MM-DD
	TrackNumber int
	TrackTotal  int
	DiscNumber  int
	DiscTotal   int
	Genre       string
	Cover       *Picture
	Back        *Picture

	// ReleaseType uses the MusicBrainz vocabulary: album, single, ep, ...
	ReleaseType string
	IDs         Identity

	// OriginalAlbum and OriginalTrack remember a single's own release when
	// it is moved into a grouped "<Artist> Singles" album, so the change
	// can be undone.
	OriginalAlbum string
	OriginalTrack int
}

// Year returns the year part of Date, or 0.
func (t Track) Year() int {
	if len(t.Date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(t.Date[:4])
	if err != nil {
		return 0
	}
	return y
}

// EffectiveAlbumArtist is the album artist, falling back to the artist.
func (t Track) EffectiveAlbumArtist() string {
	if t.AlbumArtist != "" {
		return t.AlbumArtist
	}
	return t.Artist
}

// parseNumberPair parses "3" or "3/12".
func parseNumberPair(s string) (n, total int) {
	s = strings.TrimSpace(s)
	a, b, _ := strings.Cut(s, "/")
	n, _ = strconv.Atoi(strings.TrimSpace(a))
	total, _ = strconv.Atoi(strings.TrimSpace(b))
	return n, total
}

func formatNumberPair(n, total int) string {
	if n == 0 {
		return ""
	}
	if total == 0 {
		return strconv.Itoa(n)
	}
	return strconv.Itoa(n) + "/" + strconv.Itoa(total)
}

// padTrack formats a track number with at least two digits: 01, 10, 104.
func padTrack(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%02d", n)
}

// formatTrackPair is formatNumberPair with a zero-padded track number.
func formatTrackPair(n, total int) string {
	if n == 0 {
		return ""
	}
	if total == 0 {
		return padTrack(n)
	}
	return padTrack(n) + "/" + strconv.Itoa(total)
}

func itoaOrEmpty(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}
