package source_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"your-music-library/internal/source"
	"your-music-library/internal/source/sourcetest"
)

func TestPluginProtocol(t *testing.T) {
	srv := sourcetest.New(t)
	p := srv.Plugin("fake")
	ctx := context.Background()

	h, err := p.Health(ctx)
	if err != nil || h.Name != "fake" || h.Version != "1.0" || h.Protocol != 1 {
		t.Fatalf("Health = %+v, %v", h, err)
	}

	res, err := p.Search(ctx, "off the wall", source.KindAlbum, 10)
	if err != nil || len(res) != 1 || res[0].ID != "a1" || res[0].Kind != source.KindAlbum || res[0].Quality.String() != "FLAC 16/44.1" {
		t.Fatalf("Search = %+v, %v", res, err)
	}

	album, tracks, err := p.Resolve(ctx, "a1", source.KindAlbum)
	if err != nil || album.Title != "Off the Wall" || len(tracks) != 2 || tracks[0].ISRC != "USSM17900001" {
		t.Fatalf("Resolve = %+v %+v, %v", album, tracks, err)
	}
	_, tracks, err = p.Resolve(ctx, "s1", source.KindTrack)
	if err != nil || len(tracks) != 1 || tracks[0].ID != "s1" {
		t.Errorf("Resolve track = %+v, %v", tracks, err)
	}
	_, _, err = p.Resolve(ctx, "nope", source.KindAlbum)
	if err == nil || !source.IsPermanent(err) || err.Error() != "fake resolve: unknown id nope" {
		t.Errorf("not_found error = %v", err)
	}

	dest := t.TempDir()
	file, err := p.Download(ctx, "t1", dest)
	if err != nil || file != filepath.Join(dest, "t1.flac") {
		t.Fatalf("Download = %q, %v", file, err)
	}
	if data, _ := os.ReadFile(file); len(data) == 0 {
		t.Error("downloaded file is empty")
	}
	if left, _ := filepath.Glob(filepath.Join(dest, ".download-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	if _, err := p.Download(ctx, "t1", dest); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second download over an existing file: %v", err)
	}
	if _, err := p.Download(ctx, "a1", t.TempDir()); err == nil || !source.IsPermanent(err) {
		t.Errorf("permanent error = %v", err)
	}
}

func TestTemporaryErrors(t *testing.T) {
	srv := sourcetest.New(t)
	srv.SetFailOnce(true)
	p := srv.Plugin("fake")
	_, err := p.Download(context.Background(), "s1", t.TempDir())
	if err == nil || source.IsPermanent(err) || source.IsOffline(err) || !strings.Contains(err.Error(), "temporary network error") {
		t.Fatalf("temporary error = %v", err)
	}
	if _, err := p.Download(context.Background(), "s1", t.TempDir()); err != nil {
		t.Errorf("second try: %v", err)
	}
}

func TestOffline(t *testing.T) {
	srv := sourcetest.New(t)
	p := srv.Plugin("fake")
	srv.SetOffline(true)
	if _, err := p.Health(context.Background()); !source.IsOffline(err) {
		t.Errorf("dropped connection: %v", err)
	}
	if _, err := p.Search(context.Background(), "x", source.KindTrack, 1); !source.IsOffline(err) || source.IsPermanent(err) {
		t.Errorf("search while offline: %v", err)
	}
	srv.SetOffline(false)
	if _, err := p.Health(context.Background()); err != nil {
		t.Errorf("back online: %v", err)
	}

	srv.Close()
	if _, err := p.Health(context.Background()); !source.IsOffline(err) || !strings.Contains(err.Error(), "fake health: offline at "+srv.URL) {
		t.Errorf("connection refused: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Health(ctx); source.IsOffline(err) || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled by the caller is not offline: %v", err)
	}
}

// server answers every request with h.
func server(t *testing.T, h http.HandlerFunc) *source.Plugin {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &source.Plugin{Name: "odd", URL: srv.URL + "/"}
}

func TestHealthRejectsOtherProtocols(t *testing.T) {
	p := server(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name": "x", "version": "9", "protocol": 2}`))
	})
	if _, err := p.Health(context.Background()); err == nil || !source.IsPermanent(err) || !strings.Contains(err.Error(), "speaks protocol 2, ymlib supports 1") {
		t.Errorf("Health = %v", err)
	}
}

func TestErrorsWithoutEnvelope(t *testing.T) {
	p := server(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	_, err := p.Search(context.Background(), "x", source.KindTrack, 1)
	if err == nil || source.IsPermanent(err) || err.Error() != "odd search: 500 Internal Server Error: boom" {
		t.Errorf("plain error = %v", err)
	}
	p = server(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not json")) })
	if _, err := p.Search(context.Background(), "x", source.KindTrack, 1); err == nil || !strings.Contains(err.Error(), "invalid JSON answer") {
		t.Errorf("bad JSON = %v", err)
	}
}

func TestDownloadFileNames(t *testing.T) {
	cases := []struct {
		disposition, contentType, id, want string
	}{
		{`attachment; filename="01 - Song.flac"`, "audio/flac", "x", "01 - Song.flac"},
		{`attachment; filename="../../etc/passwd"`, "", "x", "passwd"},
		{`attachment; filename="..\\..\\evil.mp3"`, "", "x", "evil.mp3"},
		{`attachment; filename=".hidden.flac"`, "", "x", "hidden.flac"},
		{"", "audio/mpeg", "album/7", "album_7.mp3"},
		{"", "audio/flac; charset=binary", "t1", "t1.flac"},
		{"", "", "..", "download"},
	}
	for _, c := range cases {
		p := server(t, func(w http.ResponseWriter, _ *http.Request) {
			if c.disposition != "" {
				w.Header().Set("Content-Disposition", c.disposition)
			}
			if c.contentType != "" {
				w.Header().Set("Content-Type", c.contentType)
			}
			_, _ = w.Write([]byte("data"))
		})
		dest := t.TempDir()
		got, err := p.Download(context.Background(), c.id, dest)
		if err != nil || got != filepath.Join(dest, c.want) {
			t.Errorf("%q / %q / %q: got %q, %v; want %q", c.disposition, c.contentType, c.id, got, err, c.want)
		}
	}
}

func TestDownloadShortBody(t *testing.T) {
	p := server(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("only a bit"))
	})
	dest := t.TempDir()
	if _, err := p.Download(context.Background(), "x", dest); err == nil || source.IsPermanent(err) {
		t.Errorf("short body = %v", err)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Errorf("files left after a failed download: %v", entries)
	}
}

func TestRegistryAndCheck(t *testing.T) {
	srv := sourcetest.New(t)
	on, off := srv.Plugin("b-on"), &source.Plugin{Name: "a-off", URL: "http://127.0.0.1:1", Enabled: false}
	reg := source.NewRegistry([]*source.Plugin{on, off})
	if all := reg.All(); len(all) != 2 || all[0] != off {
		t.Errorf("All = %v", all)
	}
	if en := reg.Enabled(); len(en) != 1 || en[0] != on {
		t.Errorf("Enabled = %v", en)
	}
	if _, err := reg.Get("nope"); err == nil || !strings.Contains(err.Error(), "ymlib plugin list") {
		t.Errorf("Get = %v", err)
	}
	st := source.Check(context.Background(), reg.All())
	if st[0].Online() || !source.IsOffline(st[0].Err) || !st[1].Online() || st[1].Health.Name != "fake" {
		t.Errorf("Check = %+v", st)
	}
}

func TestRefAndQuality(t *testing.T) {
	r, err := source.ParseRef("fake:a1")
	if err != nil || r.Source != "fake" || r.ID != "a1" || r.String() != "fake:a1" {
		t.Errorf("ParseRef = %+v, %v", r, err)
	}
	for _, bad := range []string{"", "fake", ":a1", "fake:"} {
		if _, err := source.ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) accepted", bad)
		}
	}
	if got := (source.Quality{Format: "flac", BitDepth: 24, SampleRate: 96000}).String(); got != "FLAC 24/96" {
		t.Errorf("quality = %q", got)
	}
	if got := (source.Quality{Format: "mp3", BitrateKbps: 320}).String(); got != "MP3 320" {
		t.Errorf("quality = %q", got)
	}
}
