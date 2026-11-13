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
	for _, want := range []string{"library_path:", "path_template:", "max_age_days: 180"} {
		if !strings.Contains(out, want) {
			t.Errorf("config show missing %q:\n%s", want, out)
		}
	}
}

func TestOldSettingsAreRemovedOnLoad(t *testing.T) {
	dirs := testDirs(t)
	os.MkdirAll(dirs.Config, 0o755)
	os.WriteFile(dirs.ConfigFile(), []byte("# mine\nactive_preset: car\nsingles: grouped\npresets:\n  car:\n    fields: [Title]\nlibrary_path: /music\n"), 0o644)

	out, err := run(t, dirs, "config", "show")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "note: removed active_preset, singles, presets") {
		t.Errorf("no note:\n%s", out)
	}
	data, _ := os.ReadFile(dirs.ConfigFile())
	if strings.Contains(string(data), "preset") || strings.Contains(string(data), "singles") || !strings.Contains(string(data), "# mine") {
		t.Errorf("config file:\n%s", data)
	}
	if _, err := run(t, dirs, "preset", "list"); err == nil {
		t.Error("the preset command was removed")
	}
}

func TestInvalidConfigIsReported(t *testing.T) {
	dirs := testDirs(t)
	os.MkdirAll(dirs.Config, 0o755)
	os.WriteFile(dirs.ConfigFile(), []byte("queue:\n  parallel: 0\n"), 0o644)
	if _, err := run(t, dirs, "config", "show"); err == nil || !strings.Contains(err.Error(), "queue.parallel") {
		t.Errorf("expected queue.parallel validation error, got %v", err)
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
