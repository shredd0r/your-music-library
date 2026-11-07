// Package sourcetest is an in-process fake plugin server for tests. Its
// catalog is fixed; downloads serve the tiny audio fixtures.
package sourcetest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"your-music-library/internal/source"
	"your-music-library/internal/testutil"
)

var flac = source.Quality{Format: "flac", BitDepth: 16, SampleRate: 44100}

// Catalog: album a1 with tracks t1 and t2, single s1 (MP3), and "long",
// which claims 5 minutes although the fixture is 0.1 s.
var catalog = map[string]source.Item{
	"a1":   {ID: "a1", Kind: source.KindAlbum, Title: "Off the Wall", Artist: "Michael Jackson", Year: 1979, TrackCount: 2, Quality: flac},
	"t1":   {ID: "t1", Kind: source.KindTrack, Title: "Rock With You", Artist: "Michael Jackson", Album: "Off the Wall", Year: 1979, TrackNumber: 2, DiscNumber: 1, ISRC: "USSM17900001", Quality: flac},
	"t2":   {ID: "t2", Kind: source.KindTrack, Title: "Off the Wall", Artist: "Michael Jackson", Album: "Off the Wall", Year: 1979, TrackNumber: 5, DiscNumber: 1, Quality: flac},
	"s1":   {ID: "s1", Kind: source.KindTrack, Title: "Never Gonna Give You Up", Artist: "Rick Astley", Album: "Never Gonna Give You Up", Year: 1987, TrackNumber: 1, Quality: source.Quality{Format: "mp3"}},
	"long": {ID: "long", Kind: source.KindTrack, Title: "Long Song", Artist: "Nobody", Album: "Long", TrackNumber: 1, DurationSec: 300, Quality: flac},
}

var albumTracks = map[string][]string{"a1": {"t1", "t2"}}

// Server is a running fake plugin.
type Server struct {
	*httptest.Server
	audio map[string][]byte

	mu       sync.Mutex
	offline  bool
	failOnce bool
	failed   map[string]bool
	corrupt  bool
	requests map[string]int
}

// New starts a fake plugin; it stops when the test ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		audio:    map[string][]byte{"flac": testutil.Audio(t, "flac"), "mp3": testutil.Audio(t, "mp3")},
		failed:   map[string]bool{},
		requests: map[string]int{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Plugin returns a registered, enabled plugin named name for this server.
func (s *Server) Plugin(name string) *source.Plugin {
	return &source.Plugin{Name: name, URL: s.URL, Enabled: true}
}

// SetOffline makes the server drop every connection without answering,
// as if it weren't running.
func (s *Server) SetOffline(v bool) { s.mu.Lock(); s.offline = v; s.mu.Unlock() }

// SetFailOnce makes the first download of each id fail with a temporary
// error.
func (s *Server) SetFailOnce(v bool) { s.mu.Lock(); s.failOnce = v; s.mu.Unlock() }

// SetCorrupt makes downloads serve garbage instead of audio.
func (s *Server) SetCorrupt(v bool) { s.mu.Lock(); s.corrupt = v; s.mu.Unlock() }

// Requests returns how often path (e.g. "/download") was requested.
func (s *Server) Requests(path string) int { s.mu.Lock(); defer s.mu.Unlock(); return s.requests[path] }

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	offline := s.offline
	s.requests[r.URL.Path]++
	s.mu.Unlock()
	if offline {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close() // the client sees a dropped connection
				return
			}
		}
		panic(http.ErrAbortHandler)
	}

	var req struct {
		Query string      `json:"query"`
		Kind  source.Kind `json:"kind"`
		ID    string      `json:"id"`
	}
	if r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			fail(w, http.StatusBadRequest, source.CodePermanent, "bad request: "+err.Error())
			return
		}
	}
	switch r.Method + " " + r.URL.Path {
	case "GET /health":
		answer(w, source.Health{Name: "fake", Version: "1.0", Protocol: source.ProtocolVersion})
	case "POST /search":
		q := strings.ToLower(req.Query)
		results := []source.Item{}
		for _, id := range []string{"a1", "t1", "t2", "s1", "long"} {
			it := catalog[id]
			if it.Kind == req.Kind && strings.Contains(strings.ToLower(it.Title+" "+it.Artist), q) {
				results = append(results, it)
			}
		}
		answer(w, map[string]any{"results": results})
	case "POST /resolve":
		it, ok := catalog[req.ID]
		if !ok {
			fail(w, http.StatusNotFound, source.CodeNotFound, "unknown id "+req.ID)
			return
		}
		tracks := []source.Item{}
		for _, id := range albumTracks[req.ID] {
			tracks = append(tracks, catalog[id])
		}
		answer(w, map[string]any{"item": it, "tracks": tracks})
	case "POST /download":
		s.download(w, req.ID)
	default:
		fail(w, http.StatusNotFound, source.CodePermanent, "no endpoint "+r.Method+" "+r.URL.Path)
	}
}

func (s *Server) download(w http.ResponseWriter, id string) {
	it, ok := catalog[id]
	if !ok {
		fail(w, http.StatusNotFound, source.CodeNotFound, "unknown id "+id)
		return
	}
	if it.Kind != source.KindTrack {
		fail(w, http.StatusUnprocessableEntity, source.CodePermanent, "cannot download "+id)
		return
	}
	s.mu.Lock()
	failNow := s.failOnce && !s.failed[id]
	s.failed[id] = true
	corrupt := s.corrupt
	s.mu.Unlock()
	if failNow {
		fail(w, http.StatusServiceUnavailable, source.CodeTemporary, "temporary network error")
		return
	}
	data := s.audio[it.Quality.Format]
	if corrupt {
		data = []byte("this is not audio")
	}
	w.Header().Set("Content-Type", map[string]string{"flac": "audio/flac", "mp3": "audio/mpeg"}[it.Quality.Format])
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Content-Disposition", `attachment; filename="`+id+"."+it.Quality.Format+`"`)
	_, _ = w.Write(data) // the client reports a short body
}

func answer(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) // the client reports a broken answer
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg}})
}
