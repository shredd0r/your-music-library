// Package source talks to download source plugins. A plugin is an HTTP
// server that the user runs; ymlib only keeps its address (config
// `plugins.registered`) and acts as a client. The protocol is described in
// docs/plugin-protocol.md:
//
//	GET  /health                      -> {"name", "version", "protocol": 1}
//	POST /search   {query, kind, limit} -> {"results": [Item...]}
//	POST /resolve  {id, kind}           -> {"item": Item, "tracks": [Item...]}
//	POST /download {id}                 -> the file as the response body
//
// Errors are non-2xx answers with {"error": {"code", "message"}}, code
// being not_found, temporary or permanent.
//
// ymlib does not ship plugins for any service. Plugin authors are
// responsible for using services in line with their terms.
package source

import (
	"fmt"
	"strings"
	"time"
)

// ProtocolVersion is the plugin protocol this build speaks.
const ProtocolVersion = 1

// Kind is what a search result or job refers to.
type Kind string

const (
	KindTrack Kind = "track"
	KindAlbum Kind = "album"
)

// Quality describes an offered file.
type Quality struct {
	Format      string `json:"format"` // flac, mp3, ...
	BitDepth    int    `json:"bit_depth,omitempty"`
	SampleRate  int    `json:"sample_rate,omitempty"`  // Hz
	BitrateKbps int    `json:"bitrate_kbps,omitempty"` // lossy formats
}

// String formats quality like "FLAC 24/96" or "MP3 320".
func (q Quality) String() string {
	f := strings.ToUpper(q.Format)
	switch {
	case q.BitDepth > 0 && q.SampleRate > 0:
		khz := float64(q.SampleRate) / 1000
		return fmt.Sprintf("%s %d/%s", f, q.BitDepth, strings.TrimSuffix(fmt.Sprintf("%.1f", khz), ".0"))
	case q.BitrateKbps > 0:
		return fmt.Sprintf("%s %d", f, q.BitrateKbps)
	}
	return f
}

// Item is a track or album offered by a source.
type Item struct {
	ID          string  `json:"id"`
	Kind        Kind    `json:"kind"`
	Title       string  `json:"title"`
	Artist      string  `json:"artist"`
	Album       string  `json:"album,omitempty"`
	AlbumArtist string  `json:"album_artist,omitempty"`
	Year        int     `json:"year,omitempty"`
	TrackNumber int     `json:"track_number,omitempty"`
	DiscNumber  int     `json:"disc_number,omitempty"`
	TrackCount  int     `json:"track_count,omitempty"`
	DurationSec int     `json:"duration_sec,omitempty"`
	ISRC        string  `json:"isrc,omitempty"`
	Explicit    bool    `json:"explicit,omitempty"`
	CoverURL    string  `json:"cover_url,omitempty"`
	Quality     Quality `json:"quality"`
}

// Duration is the item's length, 0 if unknown.
func (it Item) Duration() time.Duration { return time.Duration(it.DurationSec) * time.Second }

// Health is a plugin's answer to GET /health.
type Health struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
}

// Ref names an item in a source: "<source>:<id>".
type Ref struct {
	Source string
	ID     string
}

func (r Ref) String() string { return r.Source + ":" + r.ID }

// ParseRef parses "<source>:<id>".
func ParseRef(s string) (Ref, error) {
	src, id, ok := strings.Cut(s, ":")
	if !ok || src == "" || id == "" {
		return Ref{}, fmt.Errorf("%q is not a <source>:<id> reference", s)
	}
	return Ref{Source: src, ID: id}, nil
}
