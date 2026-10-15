package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"your-music-library/internal/tags"
	"your-music-library/internal/testutil"
)

func downloadApp(t *testing.T) (*app, string) {
	t.Helper()
	srv := fakeMusicBrainz(t)
	dirs := testDirs(t)
	lib := filepath.Join(t.TempDir(), "music")
	os.MkdirAll(lib, 0o755)
	os.MkdirAll(dirs.Config, 0o755)
	cfg := "library_path: " + lib + "\nproviders: [musicbrainz]\nsources_dir: " + testutil.FakeSourceDir(t) + "\n"
	os.WriteFile(dirs.ConfigFile(), []byte(cfg), 0o644)
	return &app{dirs: dirs, endpoints: endpoints{musicbrainz: srv.URL + "/ws/2", coverArt: srv.URL + "/caa", minInterval: time.Millisecond}}, lib
}

func TestSearchDownloadQueue(t *testing.T) {
	a, lib := downloadApp(t)

	out, err := runApp(t, a, "sources")
	if err != nil || !strings.Contains(out, "fake") || !strings.Contains(out, "ok") {
		t.Fatalf("sources: %v\n%s", err, out)
	}

	out, err = runApp(t, a, "search", "--album", "off the wall")
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
	a, lib := downloadApp(t)
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
