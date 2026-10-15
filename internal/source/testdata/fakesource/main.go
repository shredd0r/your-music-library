// Command fakesource is a source plugin used only by tests. Its catalog is
// fixed; "downloads" copy the tiny audio fixtures from $YMLIB_FAKE_AUDIO.
//
// Failure switches (environment):
//
//	YMLIB_FAKE_FAIL_ONCE=<dir>  first download of each id fails (marker files in dir)
//	YMLIB_FAKE_CORRUPT=1        downloads write garbage
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type quality struct {
	Format     string `json:"format"`
	BitDepth   int    `json:"bit_depth,omitempty"`
	SampleRate int    `json:"sample_rate,omitempty"`
}

type item struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	Title       string  `json:"title"`
	Artist      string  `json:"artist"`
	Album       string  `json:"album,omitempty"`
	Year        int     `json:"year,omitempty"`
	TrackNumber int     `json:"track_number,omitempty"`
	DiscNumber  int     `json:"disc_number,omitempty"`
	TrackCount  int     `json:"track_count,omitempty"`
	DurationSec int     `json:"duration_sec,omitempty"`
	ISRC        string  `json:"isrc,omitempty"`
	Explicit    bool    `json:"explicit,omitempty"`
	Quality     quality `json:"quality"`
	tracks      []string
}

var flac = quality{Format: "flac", BitDepth: 16, SampleRate: 44100}

var catalog = map[string]item{
	"a1": {ID: "a1", Kind: "album", Title: "Off the Wall", Artist: "Michael Jackson", Year: 1979, TrackCount: 2, Quality: flac, tracks: []string{"t1", "t2"}},
	"t1": {ID: "t1", Kind: "track", Title: "Rock With You", Artist: "Michael Jackson", Album: "Off the Wall", Year: 1979, TrackNumber: 2, DiscNumber: 1, ISRC: "USSM17900001", Quality: flac},
	"t2": {ID: "t2", Kind: "track", Title: "Off the Wall", Artist: "Michael Jackson", Album: "Off the Wall", Year: 1979, TrackNumber: 5, DiscNumber: 1, Quality: flac},
	"s1": {ID: "s1", Kind: "track", Title: "Never Gonna Give You Up", Artist: "Rick Astley", Album: "Never Gonna Give You Up", Year: 1987, TrackNumber: 1, Quality: quality{Format: "mp3"}, Explicit: false},
	// claims 5 minutes; the fixture is 0.1 s, so verification must fail
	"long": {ID: "long", Kind: "track", Title: "Long Song", Artist: "Nobody", Album: "Long", TrackNumber: 1, DurationSec: 300, Quality: flac},
}

func main() {
	if len(os.Args) < 2 {
		fail("usage: fakesource <command>")
	}
	var req map[string]any
	json.NewDecoder(os.Stdin).Decode(&req)
	str := func(k string) string { s, _ := req[k].(string); return s }

	switch os.Args[1] {
	case "info":
		out(map[string]any{"protocol": 1, "name": "fake", "version": "1.0"})
	case "search":
		q, kind := strings.ToLower(str("query")), str("kind")
		results := []item{}
		for _, id := range []string{"a1", "t1", "t2", "s1", "long"} {
			it := catalog[id]
			if it.Kind == kind && strings.Contains(strings.ToLower(it.Title+" "+it.Artist), q) {
				results = append(results, it)
			}
		}
		out(map[string]any{"results": results})
	case "resolve":
		it, ok := catalog[str("id")]
		if !ok {
			out(map[string]any{"error": "unknown id " + str("id")})
			return
		}
		var tracks []item
		for _, t := range it.tracks {
			tracks = append(tracks, catalog[t])
		}
		out(map[string]any{"item": it, "tracks": tracks})
	case "download":
		id, dest := str("id"), str("dest_dir")
		it, ok := catalog[id]
		if !ok || it.Kind != "track" {
			fail("cannot download " + id)
		}
		if dir := os.Getenv("YMLIB_FAKE_FAIL_ONCE"); dir != "" {
			marker := filepath.Join(dir, "failed-"+id)
			if _, err := os.Stat(marker); err != nil {
				os.WriteFile(marker, nil, 0o644)
				fail("temporary network error")
			}
		}
		data, err := os.ReadFile(filepath.Join(os.Getenv("YMLIB_FAKE_AUDIO"), "tiny."+it.Quality.Format))
		if err != nil {
			fail(err.Error())
		}
		if os.Getenv("YMLIB_FAKE_CORRUPT") == "1" {
			data = []byte("this is not audio")
		}
		name := id + "." + it.Quality.Format
		if err := os.WriteFile(filepath.Join(dest, name), data, 0o644); err != nil {
			fail(err.Error())
		}
		out(map[string]any{"file": name})
	default:
		fail("unknown command " + os.Args[1])
	}
}

func out(v any) { json.NewEncoder(os.Stdout).Encode(v) }

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
