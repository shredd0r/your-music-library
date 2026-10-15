// Package source runs download source plugins. A plugin is an executable
// in the sources directory (or a symlink to one), named after its file.
//
// Every plugin answers info as a single run:
//
//	<plugin> info   with {} on stdin -> {"protocol": 1|2, "name": "...", "version": "..."}
//
// Protocol 1: the plugin is started once per request, as
// `<plugin> <command>` with a JSON request on stdin, and answers with JSON
// on stdout and exit status 0. On failure it exits non-zero (stderr is
// reported) or answers {"error": "..."}.
//
// Protocol 2: the plugin keeps state between requests. ymlib starts
// `<plugin> serve` on the first request and keeps it running. Requests
// and answers are single JSON lines on stdin and stdout. A request has an
// "id", a "command" and the protocol 1 request as "params"; its answer
// repeats the id and carries the protocol 1 answer as "result":
//
//	-> {"id": 7, "command": "search", "params": {"query": "...", "kind": "track", "limit": 20}}
//	<- {"id": 7, "result": {"results": [...]}}     or {"id": 7, "error": "..."}
//
// Requests may overlap (parallel downloads), and answers may come in any
// order. stdout carries only answers; logs go to stderr. When ymlib no
// longer needs the plugin (it quits, a command ends, or the plugin has been
// idle for sources.idle_minutes) it sends {"id": n, "command": "close"} and
// closes stdin. The plugin cleans up, may answer {"id": n}, and exits; after
// sources.close_seconds it is killed. If it crashes, its requests fail (downloads
// are retried) and the next request starts it again.
//
// Commands (both protocols):
//
//	search    {"query": "...", "kind": "track|album", "limit": 20} -> {"results": [Item...]}
//	resolve   {"id": "...", "kind": "track|album"}  -> {"item": Item, "tracks": [Item...]}
//	download  {"id": "...", "dest_dir": "/abs/dir"} -> {"file": "name.flac"}  (inside dest_dir)
//
// ymlib does not ship plugins for any service. Plugin authors are
// responsible for using services in line with their terms.
package source

import (
	"fmt"
	"strings"
	"time"
)

// ProtocolVersion is the newest plugin protocol this build speaks.
const ProtocolVersion = 2

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

// Info is a plugin's self-description.
type Info struct {
	Protocol int    `json:"protocol"`
	Name     string `json:"name"`
	Version  string `json:"version"`
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
