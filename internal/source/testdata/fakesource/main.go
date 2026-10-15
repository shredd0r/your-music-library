// Command fakesource is a source plugin used only by tests. Its catalog is
// fixed; "downloads" copy the tiny audio fixtures from $YMLIB_FAKE_AUDIO.
//
// Switches (environment):
//
//	YMLIB_FAKE_FAIL_ONCE=<dir>  first download of each id fails (marker files in dir)
//	YMLIB_FAKE_CORRUPT=1        downloads write garbage
//	YMLIB_FAKE_SERVE=1          speak protocol 2 (`fakesource serve`)
//	YMLIB_FAKE_STATE=<dir>      protocol 2: write closed-<pid> to dir on close
//	YMLIB_FAKE_IGNORE_CLOSE=1   protocol 2: ignore close and end of input
//
// Special search queries: "whoami" returns one item with the process id
// as ID and the number of requests this process has served as Title;
// "slow" answers after 300 ms; "crash" exits with status 3.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

var served atomic.Int64

func main() {
	if len(os.Args) < 2 {
		fail("usage: fakesource <command>")
	}
	if os.Args[1] == "serve" {
		serve()
		return
	}
	var req map[string]any
	json.NewDecoder(os.Stdin).Decode(&req)
	res, fatal := handle(os.Args[1], req)
	if fatal != "" {
		fail(fatal)
	}
	out(res)
}

// serve is protocol 2: JSON lines, each request handled concurrently.
func serve() {
	var mu sync.Mutex
	enc := json.NewEncoder(os.Stdout)
	write := func(v map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		enc.Encode(v)
	}
	ignoreClose := os.Getenv("YMLIB_FAKE_IGNORE_CLOSE") == "1"
	var wg sync.WaitGroup
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var msg struct {
			ID      int64          `json:"id"`
			Command string         `json:"command"`
			Params  map[string]any `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &msg) != nil {
			continue
		}
		if msg.Command == "close" {
			if ignoreClose {
				continue
			}
			wg.Wait()
			if dir := os.Getenv("YMLIB_FAKE_STATE"); dir != "" {
				os.WriteFile(filepath.Join(dir, fmt.Sprintf("closed-%d", os.Getpid())), nil, 0o644)
			}
			write(map[string]any{"id": msg.ID})
			return
		}
		served.Add(1)
		wg.Go(func() {
			res, fatal := handle(msg.Command, msg.Params)
			if fatal != "" {
				write(map[string]any{"id": msg.ID, "error": fatal})
				return
			}
			if e, ok := res["error"]; ok {
				write(map[string]any{"id": msg.ID, "error": e})
				return
			}
			write(map[string]any{"id": msg.ID, "result": res})
		})
	}
	if ignoreClose {
		select {} // a stuck plugin: only a kill stops it
	}
	wg.Wait()
}

// handle answers one request. fatal is a failure reported as a non-zero
// exit in protocol 1.
func handle(command string, req map[string]any) (res map[string]any, fatal string) {
	str := func(k string) string { s, _ := req[k].(string); return s }

	switch command {
	case "info":
		protocol := 1
		if os.Getenv("YMLIB_FAKE_SERVE") == "1" {
			protocol = 2
		}
		return map[string]any{"protocol": protocol, "name": "fake", "version": "1.0"}, ""
	case "search":
		q, kind := strings.ToLower(str("query")), str("kind")
		switch q {
		case "whoami":
			return map[string]any{"results": []item{{ID: strconv.Itoa(os.Getpid()), Kind: kind, Title: strconv.FormatInt(served.Load(), 10)}}}, ""
		case "slow":
			time.Sleep(300 * time.Millisecond)
		case "crash":
			fmt.Fprintln(os.Stderr, "fake crashed")
			os.Exit(3)
		}
		results := []item{}
		for _, id := range []string{"a1", "t1", "t2", "s1", "long"} {
			it := catalog[id]
			if it.Kind == kind && strings.Contains(strings.ToLower(it.Title+" "+it.Artist), q) {
				results = append(results, it)
			}
		}
		return map[string]any{"results": results}, ""
	case "resolve":
		it, ok := catalog[str("id")]
		if !ok {
			return map[string]any{"error": "unknown id " + str("id")}, ""
		}
		var tracks []item
		for _, t := range it.tracks {
			tracks = append(tracks, catalog[t])
		}
		return map[string]any{"item": it, "tracks": tracks}, ""
	case "download":
		id, dest := str("id"), str("dest_dir")
		it, ok := catalog[id]
		if !ok || it.Kind != "track" {
			return nil, "cannot download " + id
		}
		if dir := os.Getenv("YMLIB_FAKE_FAIL_ONCE"); dir != "" {
			marker := filepath.Join(dir, "failed-"+id)
			if _, err := os.Stat(marker); err != nil {
				os.WriteFile(marker, nil, 0o644)
				return nil, "temporary network error"
			}
		}
		data, err := os.ReadFile(filepath.Join(os.Getenv("YMLIB_FAKE_AUDIO"), "tiny."+it.Quality.Format))
		if err != nil {
			return nil, err.Error()
		}
		if os.Getenv("YMLIB_FAKE_CORRUPT") == "1" {
			data = []byte("this is not audio")
		}
		name := id + "." + it.Quality.Format
		if err := os.WriteFile(filepath.Join(dest, name), data, 0o644); err != nil {
			return nil, err.Error()
		}
		return map[string]any{"file": name}, ""
	}
	return nil, "unknown command " + command
}

func out(v any) { json.NewEncoder(os.Stdout).Encode(v) }

func fail(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
