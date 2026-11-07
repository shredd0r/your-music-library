package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"your-music-library/internal/source/sourcetest"
	"your-music-library/internal/tags"
	"your-music-library/internal/testutil"
)

// downloadApp returns an app whose config registers the fake plugin as
// "fake".
func downloadApp(t *testing.T) (*app, string, *sourcetest.Server) {
	t.Helper()
	srv := fakeMusicBrainz(t)
	plugin := sourcetest.New(t)
	dirs := testDirs(t)
	lib := filepath.Join(t.TempDir(), "music")
	os.MkdirAll(lib, 0o755)
	os.MkdirAll(dirs.Config, 0o755)
	cfg := "library_path: " + lib + "\nproviders: [musicbrainz]\nplugins:\n  registered:\n    fake: {url: \"" + plugin.URL + "\", enabled: true}\n"
	os.WriteFile(dirs.ConfigFile(), []byte(cfg), 0o644)
	return &app{dirs: dirs, endpoints: endpoints{musicbrainz: srv.URL + "/ws/2", coverArt: srv.URL + "/caa", minInterval: time.Millisecond}}, lib, plugin
}

func TestSearchDownloadQueue(t *testing.T) {
	a, lib, _ := downloadApp(t)

	out, err := runApp(t, a, "search", "--album", "off the wall")
	if err != nil || !strings.Contains(out, "fake:a1") || !strings.Contains(out, "Off the Wall (1979), 2 tracks") || !strings.Contains(out, "FLAC 16/44.1") {
		t.Fatalf("search: %v\n%s", err, out)
	}

	out, err = runApp(t, a, "download", "--album", "fake:a1")
	if err != nil {
		t.Fatalf("download: %v\n%s", err, out)
	}
	if !strings.Contains(out, "imported 2, already in library 0, failed 0") {
		t.Errorf("download output:\n%s", out)
	}
	// identified through (fake) MusicBrainz: release title and date
	got, err := tags.Read(filepath.Join(lib, "Michael Jackson", "Off the Wall", "02 - Rock With You.flac"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got.Date != "1979-08-10" || got.IDs.MBReleaseID != otwMBID || got.IDs.Source != "fake" || got.Cover == nil {
		t.Errorf("tags = %+v", got)
	}

	out, _ = runApp(t, a, "search", "rock with you")
	if !strings.Contains(out, "✓") {
		t.Errorf("downloaded track not flagged in search:\n%s", out)
	}
	out, _ = runApp(t, a, "queue")
	if !strings.Contains(out, "done") || !strings.Contains(out, "2 imported") {
		t.Errorf("queue:\n%s", out)
	}
	out, _ = runApp(t, a, "download", "--album", "fake:a1")
	if !strings.Contains(out, "already downloaded") {
		t.Errorf("repeat download:\n%s", out)
	}
	out, _ = runApp(t, a, "queue", "clear")
	if !strings.Contains(out, "3 jobs removed") {
		t.Errorf("clear:\n%s", out)
	}
}

func TestImportCommand(t *testing.T) {
	a, lib, _ := downloadApp(t)
	src := t.TempDir()
	p := testutil.CopyAudio(t, "flac", filepath.Join(src, "rip", "x.flac"))
	tags.Write(p, tags.Track{Title: "Rock With You", Artist: "Michael Jackson", Album: "Off The Wall"})

	out, err := runApp(t, a, "import", src)
	if err != nil || !strings.Contains(out, "Michael Jackson/Off the Wall/02 - Rock With You.flac") || !strings.Contains(out, "Preview only") {
		t.Fatalf("preview: %v\n%s", err, out)
	}
	if out, err = runApp(t, a, "import", src, "--apply"); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(lib, "Michael Jackson", "Off the Wall", "02 - Rock With You.flac")); err != nil {
		t.Errorf("not imported: %v\n%s", err, out)
	}
	if _, err := os.Stat(p); err != nil {
		t.Error("import without --move must keep the source")
	}
	out, _ = runApp(t, a, "import", src)
	if !strings.Contains(out, "already in library") {
		t.Errorf("second import:\n%s", out)
	}
	if _, err := runApp(t, a, "import", lib); err == nil {
		t.Error("importing from inside the library should be refused")
	}
}

func TestPluginCommands(t *testing.T) {
	a, _, _ := downloadApp(t)
	out, err := runApp(t, a, "plugin", "list")
	if err != nil || !strings.Contains(out, "fake") || !strings.Contains(out, "online (fake 1.0)") || !strings.Contains(out, "yes") {
		t.Fatalf("list: %v\n%s", err, out)
	}

	other := sourcetest.New(t)
	out, err = runApp(t, a, "plugin", "add", "nas", other.URL)
	if err != nil || !strings.Contains(out, "Registered nas") || !strings.Contains(out, "online: fake 1.0") {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if _, err := runApp(t, a, "plugin", "add", "nas", other.URL); err == nil {
		t.Error("adding a name twice")
	}
	out, err = runApp(t, a, "plugin", "add", "later", "http://127.0.0.1:1")
	if err != nil || !strings.Contains(out, "offline now") {
		t.Fatalf("add offline: %v\n%s", err, out)
	}

	if out, err = runApp(t, a, "plugin", "disable", "fake"); err != nil || !strings.Contains(out, "fake is disabled") {
		t.Fatalf("disable: %v\n%s", err, out)
	}
	out, _ = runApp(t, a, "plugin")
	for _, want := range []string{"later", "offline", "nas", "no"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	// search uses enabled plugins only: nas (online) and later (offline, warned)
	out, err = runApp(t, a, "search", "rock")
	if err != nil || !strings.Contains(out, "nas:t1") || strings.Contains(out, "fake:t1") || !strings.Contains(out, "later search: offline") {
		t.Errorf("search: %v\n%s", err, out)
	}
	// a disabled plugin can still be searched by name
	if out, _ = runApp(t, a, "search", "--plugin", "fake", "rock"); !strings.Contains(out, "fake:t1") {
		t.Errorf("search --plugin:\n%s", out)
	}

	if out, err = runApp(t, a, "plugin", "remove", "later"); err != nil || !strings.Contains(out, "Removed later") {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if _, err := runApp(t, a, "plugin", "enable", "later"); err == nil {
		t.Error("enabling a removed plugin")
	}
}

func TestDownloadWaitsForOfflinePlugin(t *testing.T) {
	a, lib, srv := downloadApp(t)
	srv.SetOffline(true)
	out, err := runApp(t, a, "download", "fake:s1")
	if err != nil || !strings.Contains(out, "waiting for a plugin 1") || !strings.Contains(out, "Start the plugin") {
		t.Fatalf("offline download: %v\n%s", err, out)
	}
	if out, _ = runApp(t, a, "queue"); !strings.Contains(out, "waiting") || !strings.Contains(out, "offline") {
		t.Errorf("queue:\n%s", out)
	}
	srv.SetOffline(false)
	if out, err = runApp(t, a, "queue", "run"); err != nil || !strings.Contains(out, "imported 1") {
		t.Fatalf("queue run: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(lib, "Rick Astley")); err != nil {
		t.Error(err)
	}
}

func TestOldPluginSettingsGetAHint(t *testing.T) {
	a, _, _ := downloadApp(t)
	os.WriteFile(a.dirs.ConfigFile(), []byte("sources_dir: /x\n"), 0o644)
	if _, err := runApp(t, a, "plugin", "list"); err == nil || !strings.Contains(err.Error(), "ymlib plugin add") {
		t.Errorf("old config: %v", err)
	}
}
