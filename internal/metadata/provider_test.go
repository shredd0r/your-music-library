package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"your-music-library/internal/cache"
	"your-music-library/internal/store"
)

// fixtureServer serves files from testdata/<dir> by a route table and
// records each request. A route key is a path, optionally followed by
// "|text" to match only requests whose decoded query contains text.
type fixtureServer struct {
	*httptest.Server
	requests atomic.Int32
	lastReq  atomic.Pointer[http.Request]
}

func newFixtureServer(t *testing.T, dir string, routes map[string]string) *fixtureServer {
	t.Helper()
	fs := &fixtureServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fs.requests.Add(1)
		fs.lastReq.Store(r)
		query, _ := url.QueryUnescape(r.URL.RawQuery)
		file, ok := "", false
		for key, f := range routes {
			p, text, hasText := strings.Cut(key, "|")
			if p == r.URL.Path && (!hasText || strings.Contains(query, text)) && (!ok || hasText) {
				file, ok = f, true
			}
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join("testdata", dir, file))
		if err != nil {
			t.Errorf("fixture %s: %v", file, err)
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
	t.Cleanup(fs.Close)
	return fs
}

func fastOpts() HTTPOptions {
	return HTTPOptions{UserAgent: "ymlib-test", MinInterval: time.Millisecond}
}

func TestDiscogsSearchAndRelease(t *testing.T) {
	srv := newFixtureServer(t, "discogs", map[string]string{
		"/database/search": "search_offthewall.json",
		"/releases/120096": "release_120096.json",
		"/releases/249504": "release_249504.json",
	})
	d := NewDiscogs("secret-token", fastOpts(), srv.URL)
	ctx := context.Background()

	cands, err := d.Search(ctx, Query{Artist: "Michael Jackson", Album: "Off the Wall"})
	if err != nil {
		t.Fatal(err)
	}
	r := srv.lastReq.Load()
	if got := r.Header.Get("Authorization"); got != "Discogs token=secret-token" {
		t.Errorf("Authorization = %q", got)
	}
	if r.URL.Query().Get("release_title") != "Off the Wall" || r.URL.Query().Get("artist") != "Michael Jackson" {
		t.Errorf("query = %v", r.URL.Query())
	}
	if len(cands) != 2 || cands[0] != (Candidate{ID: "120096", Title: "Off The Wall", Artist: "Michael Jackson", Year: 1979, ReleaseType: "album"}) {
		t.Errorf("cands[0] = %+v", cands)
	}
	if !cands[1].Compilation || cands[1].Artist != "Various" {
		t.Errorf("cands[1] = %+v", cands[1])
	}

	rel, err := d.Release(ctx, "120096")
	if err != nil {
		t.Fatal(err)
	}
	if rel.Title != "Off The Wall" || rel.Artist != "Michael Jackson" || rel.Date != "1979-08-10" ||
		rel.Genre != "Funk / Soul" || rel.ReleaseType != "album" || rel.Barcode != "074643574518" || rel.Format != "Vinyl" {
		t.Errorf("release = %+v", rel)
	}
	if len(rel.Tracks) != 10 || rel.DiscTotal() != 1 {
		t.Fatalf("tracks = %d, discs = %d", len(rel.Tracks), rel.DiscTotal())
	}
	// vinyl sides A1..B6 are numbered 1..10
	if tr := rel.Tracks[4]; tr.Number != 5 || tr.Title != "Off The Wall" || tr.Duration != 4*time.Minute+4*time.Second || tr.Artist != "Michael Jackson" {
		t.Errorf("track 5 = %+v", tr)
	}
	if !strings.HasPrefix(rel.FrontURL, "https://i.discogs.com/") {
		t.Errorf("FrontURL = %q", rel.FrontURL)
	}

	single, err := d.Release(ctx, "249504")
	if err != nil {
		t.Fatal(err)
	}
	if single.ReleaseType != "single" || single.Date != "1987-07" || len(single.Tracks) != 2 || single.Tracks[1].Number != 2 {
		t.Errorf("single = %+v", single)
	}
}

func TestDiscogsHelpers(t *testing.T) {
	credit, names := discogsCredit([]discogsArtist{{Name: "Simon (3)", Join: "&"}, {Name: "Garfunkel", Join: ","}, {Name: "Prince (2)", ANV: "The Artist"}})
	if credit != "Simon & Garfunkel, The Artist" || !reflect.DeepEqual(names, []string{"Simon", "Garfunkel", "Prince"}) {
		t.Errorf("credit = %q, names = %v", credit, names)
	}
	for pos, want := range map[string][2]int{"2-03": {2, 3}, "CD1-7": {1, 7}, "1.12": {1, 12}} {
		d, n, ok := parseDiscogsPosition(pos)
		if !ok || d != want[0] || n != want[1] {
			t.Errorf("parseDiscogsPosition(%q) = %d,%d,%v", pos, d, n, ok)
		}
	}
	for _, pos := range []string{"A1", "B", "3", ""} {
		if _, _, ok := parseDiscogsPosition(pos); ok {
			t.Errorf("%q should be numbered sequentially", pos)
		}
	}
	for in, want := range map[string]string{"1987-07-00": "1987-07", "1987-00-00": "1987", "": "1999", "2001-02-03": "2001-02-03"} {
		if got := discogsDate(in, 1999); got != want {
			t.Errorf("discogsDate(%q) = %q, want %q", in, got, want)
		}
	}
	if parseClock("1:02:03") != time.Hour+2*time.Minute+3*time.Second || parseClock("") != 0 {
		t.Error("parseClock")
	}
}

func TestHTTPRetriesCachesAnd404(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		switch {
		case r.URL.Path == "/missing":
			http.NotFound(w, r)
		case n == 1:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.Write([]byte(`{"ok": true}`))
		}
	}))
	defer srv.Close()

	db, err := store.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	c, _ := cache.Open(ctx, db, time.Hour)
	opts := fastOpts()
	opts.Cache = c
	h := newHTTP(opts)

	var v struct{ OK bool }
	if err := h.getJSON(ctx, srv.URL+"/x", &v); err != nil || !v.OK {
		t.Fatalf("getJSON = %v %+v", err, v)
	}
	if calls.Load() != 2 {
		t.Errorf("expected one retry, got %d calls", calls.Load())
	}
	if err := h.getJSON(ctx, srv.URL+"/x", &v); err != nil || calls.Load() != 2 {
		t.Errorf("second call should come from cache (%d calls, %v)", calls.Load(), err)
	}
	if err := h.getJSON(ctx, srv.URL+"/missing", &v); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 = %v", err)
	}
}

func TestRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("{}")) }))
	defer srv.Close()
	h := newHTTP(HTTPOptions{UserAgent: "t", MinInterval: 50 * time.Millisecond})
	start := time.Now()
	for i := 0; i < 3; i++ {
		var v any
		h.getJSON(context.Background(), srv.URL, &v)
	}
	if el := time.Since(start); el < 100*time.Millisecond {
		t.Errorf("3 requests took %v; rate limit not applied", el)
	}
}

const otwMBID = "7fa492d6-3b75-4f75-9cd8-b87e4def2417"

func TestMusicBrainzSearchAndRelease(t *testing.T) {
	srv := newFixtureServer(t, "musicbrainz", map[string]string{
		"/release":             "search_release_offthewall.json",
		"/release|Never Gonna": "search_release_nggyu.json",
		"/recording":           "search_recording_nggyu.json",
		"/release/" + otwMBID:  "release_" + otwMBID + ".json",
	})
	m := NewMusicBrainz(fastOpts(), srv.URL, "https://caa.test")
	ctx := context.Background()

	cands, err := m.Search(ctx, Query{Artist: "Michael Jackson", Album: "Off the Wall"})
	if err != nil {
		t.Fatal(err)
	}
	q := srv.lastReq.Load().URL.Query()
	if q.Get("query") != `release:"Off the Wall" AND artist:"Michael Jackson"` || q.Get("fmt") != "json" {
		t.Errorf("query = %v", q)
	}
	if srv.lastReq.Load().Header.Get("User-Agent") != "ymlib-test" {
		t.Error("User-Agent missing")
	}
	if len(cands) != 5 || cands[0].ID != otwMBID || cands[0].TrackCount != 10 || cands[0].Artist != "Michael Jackson" || cands[0].ReleaseType != "album" {
		t.Errorf("cands = %+v", cands)
	}

	// lone track: releases named like the track (its single) come first,
	// then releases found through the recording; compilations are flagged
	cands, err = m.Search(ctx, Query{Artist: "Rick Astley", Tracks: []QueryTrack{{Title: "Never Gonna Give You Up"}}})
	if err != nil || len(cands) == 0 {
		t.Fatalf("lone track search: %v %v", cands, err)
	}
	if cands[0].Title != "Never Gonna Give You Up" || cands[0].ReleaseType != "single" {
		t.Errorf("first candidate should be the single: %+v", cands[0])
	}
	sawCompilation := false
	for _, c := range cands {
		sawCompilation = sawCompilation || c.Compilation
	}
	if !sawCompilation {
		t.Error("recording search results (compilations) missing")
	}

	rel, err := m.Release(ctx, otwMBID)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Title != "Off the Wall" || rel.Date != "1979-08-10" || rel.ReleaseType != "album" || rel.Genre == "" ||
		rel.Barcode != "4988010229658" || rel.Label != "Epic" || rel.Format != "CD" {
		t.Errorf("release = %+v", rel)
	}
	if rel.FrontURL != "https://caa.test/release/"+otwMBID+"/front" || rel.BackURL == "" {
		t.Errorf("cover URLs = %q %q", rel.FrontURL, rel.BackURL)
	}
	tr := rel.Tracks[1]
	if len(rel.Tracks) != 10 || tr.Number != 2 || tr.Disc != 1 || tr.Title != "Rock With You" || tr.ISRC != "USSM17900001" ||
		tr.MBRecordingID == "" || tr.Duration != 220*time.Second {
		t.Errorf("track 2 = %+v", tr)
	}
}

func TestMusicBrainzFixtureMatchesLocalTitles(t *testing.T) {
	srv := newFixtureServer(t, "musicbrainz", map[string]string{
		"/release":            "search_release_offthewall.json",
		"/release/" + otwMBID: "release_" + otwMBID + ".json",
	})
	m := NewMusicBrainz(fastOpts(), srv.URL, "")
	// titles as typically found in files: straight apostrophes, other casing
	q := Query{Artist: "Michael Jackson", Album: "Off The Wall", Tracks: []QueryTrack{
		{Title: "Don't Stop 'Til You Get Enough", Number: 1},
		{Title: "Working Day And Night", Number: 3},
		{Title: "She's Out Of My Life", Number: 7},
	}}
	res, err := (&Identifier{Providers: []Provider{m}, MinScore: 0.85, Details: 1}).Identify(context.Background(), q)
	if err != nil || res.Best == nil {
		t.Fatalf("res = %+v, %v", res, err)
	}
	for i, wantNum := range []int{1, 3, 7} {
		if tr, ok := res.Best.Track(i); !ok || tr.Number != wantNum {
			t.Errorf("query track %d matched %+v", i, tr)
		}
	}
}

func TestTopGenre(t *testing.T) {
	got := topGenre([]mbGenre{{"disco", 1}, {"contemporary r&b", 3}})
	if got != "Contemporary R&B" {
		t.Errorf("topGenre = %q", got)
	}
	if topGenre(nil, []mbGenre{{"pop", 1}}) != "Pop" || topGenre() != "" {
		t.Error("fallback to release-group genres")
	}
}
