package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"your-music-library/internal/platform"
	"your-music-library/internal/tags"
	"your-music-library/internal/testutil"
)

func testDirs(t *testing.T) platform.Dirs {
	base := t.TempDir()
	return platform.Dirs{
		Config: filepath.Join(base, "config"),
		Cache:  filepath.Join(base, "cache"),
		Data:   filepath.Join(base, "data"),
	}
}

func run(t *testing.T, dirs platform.Dirs, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCommand(dirs)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestConfigInitThenShow(t *testing.T) {
	dirs := testDirs(t)
	if _, err := run(t, dirs, "config", "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirs.ConfigFile()); err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if _, err := run(t, dirs, "config", "init"); err == nil {
		t.Error("second init without --force should fail")
	}
	if _, err := run(t, dirs, "config", "init", "--force"); err != nil {
		t.Errorf("init --force: %v", err)
	}
	out, err := run(t, dirs, "config", "show")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"library_path:", "active_preset: full", "singles: as-album", "max_age_days: 180"} {
		if !strings.Contains(out, want) {
			t.Errorf("config show missing %q:\n%s", want, out)
		}
	}
}

func TestPresetListMarksActive(t *testing.T) {
	dirs := testDirs(t)
	os.MkdirAll(dirs.Config, 0o755)
	os.WriteFile(dirs.ConfigFile(), []byte("active_preset: car\npresets:\n  car:\n    fields: [Title, Artist]\n    cover_max_px: 300\n"), 0o644)

	out, err := run(t, dirs, "preset", "list")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 { // header + car, full, mp3-player
		t.Fatalf("expected 4 lines:\n%s", out)
	}
	if !strings.HasPrefix(lines[1], "*") || !strings.Contains(lines[1], "car") || !strings.Contains(lines[1], "300px") {
		t.Errorf("car should be first and active: %q", lines[1])
	}
	if !strings.Contains(out, "mp3-player") || !strings.Contains(out, "600px") || !strings.Contains(out, "built-in") {
		t.Errorf("built-ins missing:\n%s", out)
	}
}

func TestInvalidConfigIsReported(t *testing.T) {
	dirs := testDirs(t)
	os.MkdirAll(dirs.Config, 0o755)
	os.WriteFile(dirs.ConfigFile(), []byte("singles: sometimes\n"), 0o644)
	if _, err := run(t, dirs, "preset", "list"); err == nil || !strings.Contains(err.Error(), "singles") {
		t.Errorf("expected singles validation error, got %v", err)
	}
	// path works even with a broken config, so users can find the file
	if _, err := run(t, dirs, "config", "path"); err != nil {
		t.Errorf("config path should not need a valid config: %v", err)
	}
}

func TestOrganizePreviewThenApply(t *testing.T) {
	dirs := testDirs(t)
	lib := filepath.Join(t.TempDir(), "music")
	src := testutil.CopyAudio(t, "flac", filepath.Join(lib, "inbox", "x.flac"))
	if err := tags.Write(src, tags.Track{Title: "Bad", Album: "Bad", AlbumArtist: "Michael Jackson", TrackNumber: 1}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(dirs.Config, 0o755)
	os.WriteFile(dirs.ConfigFile(), []byte("library_path: "+lib+"\n"), 0o644)

	out, err := run(t, dirs, "organize")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Michael Jackson/Bad/01 - Bad.flac") || !strings.Contains(out, "Preview only") {
		t.Errorf("preview output:\n%s", out)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("preview moved the file")
	}
	if _, err := run(t, dirs, "organize", "--apply"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(lib, "Michael Jackson", "Bad", "01 - Bad.flac")); err != nil {
		t.Errorf("file not moved: %v", err)
	}
}

func TestMissingLibraryFolderIsReported(t *testing.T) {
	dirs := testDirs(t)
	os.MkdirAll(dirs.Config, 0o755)
	os.WriteFile(dirs.ConfigFile(), []byte("library_path: /definitely/not/here\n"), 0o644)
	if _, err := run(t, dirs, "scan"); err == nil || !strings.Contains(err.Error(), "library folder") {
		t.Errorf("expected library folder error, got %v", err)
	}
}
