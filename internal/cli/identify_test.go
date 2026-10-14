package cli

import (
	"bytes"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"your-music-library/internal/secrets"
	"your-music-library/internal/tags"
	"your-music-library/internal/testutil"
)

func TestMain(m *testing.M) {
	secrets.UseMockKeychain() // never touch the real keychain
	os.Unsetenv(secrets.DiscogsToken.Env)
	os.Exit(m.Run())
}

const otwMBID = "7fa492d6-3b75-4f75-9cd8-b87e4def2417"

// fakeMusicBrainz serves recorded fixtures and a cover image.
func fakeMusicBrainz(t *testing.T) *httptest.Server {
	fixtures := filepath.Join("..", "metadata", "testdata", "musicbrainz")
	cover := testutil.JPEG(t, 800, 800, color.RGBA{1, 2, 3, 255})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws/2/release":
			http.ServeFile(w, r, filepath.Join(fixtures, "search_release_offthewall.json"))
		case "/ws/2/release/" + otwMBID:
			http.ServeFile(w, r, filepath.Join(fixtures, "release_"+otwMBID+".json"))
		case "/caa/release/" + otwMBID + "/front", "/caa/release/" + otwMBID + "/back":
			w.Write(cover)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func runApp(t *testing.T, a *app, args ...string) (string, error) {
	t.Helper()
	cmd := newRoot(a)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestIdentifyWithMusicBrainz(t *testing.T) {
	srv := fakeMusicBrainz(t)
	dirs := testDirs(t)
	lib := filepath.Join(t.TempDir(), "music")
	for i, title := range []string{"Rock With You", "Off The Wall"} {
		p := testutil.CopyAudio(t, "flac", filepath.Join(lib, "otw", []string{"a.flac", "b.flac"}[i]))
		tags.Write(p, tags.Track{Title: title, Artist: "Michael Jackson", Album: "Off The Wall"})
	}
	os.MkdirAll(dirs.Config, 0o755)
	os.WriteFile(dirs.ConfigFile(), []byte("library_path: "+lib+"\nproviders: [discogs, musicbrainz]\n"), 0o644)
	a := &app{dirs: dirs, endpoints: endpoints{musicbrainz: srv.URL + "/ws/2", coverArt: srv.URL + "/caa", minInterval: time.Millisecond}}

	out, err := runApp(t, a, "identify")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"no token", // Discogs skipped with a warning
		"✓ otw  →  Michael Jackson — Off the Wall (1979-08-10)",
		"Track Number:",
		"1 albums matched (2 files)",
		"Preview only",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}

	out, err = runApp(t, a, "identify", "--apply")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	got, _ := tags.Read(filepath.Join(lib, "otw", "b.flac"))
	if got.Album != "Off the Wall" || got.TrackNumber != 5 || got.IDs.MBReleaseID != otwMBID || got.Cover == nil {
		t.Errorf("tags after apply = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(lib, "otw", "cover.jpg")); err != nil {
		t.Error("cover.jpg not saved")
	}
	out, _ = runApp(t, a, "identify")
	if !strings.Contains(out, "2 already identified") {
		t.Errorf("rerun:\n%s", out)
	}
}

func TestAuthDiscogs(t *testing.T) {
	a := &app{dirs: testDirs(t)}
	cmd := newRoot(a)
	cmd.SetIn(strings.NewReader("tok-123\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"auth", "discogs"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if v, src, _ := secrets.Get(secrets.DiscogsToken); v != "tok-123" || src != secrets.SourceKeychain {
		t.Errorf("stored %q from %q", v, src)
	}
	out2, _ := runApp(t, a, "auth", "status")
	if !strings.Contains(out2, "token from keychain") {
		t.Errorf("status:\n%s", out2)
	}
	runApp(t, a, "auth", "discogs", "--remove")
	if v, _, _ := secrets.Get(secrets.DiscogsToken); v != "" {
		t.Error("token not removed")
	}
}
