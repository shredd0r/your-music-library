package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"your-music-library/internal/platform"
	"your-music-library/internal/secrets"
)

func testDirs(t *testing.T) platform.Dirs {
	base := t.TempDir()
	return platform.Dirs{Config: filepath.Join(base, "config"), Cache: filepath.Join(base, "cache"), Data: filepath.Join(base, "data")}
}

func TestFirstStartWritesDefaultConfig(t *testing.T) {
	secrets.UseMockKeychain()
	t.Setenv(secrets.DiscogsToken.Env, "")
	dirs := testDirs(t)
	lib := filepath.Join(t.TempDir(), "music")
	t.Setenv("HOME", filepath.Dir(lib)) // the default library_path is under ~

	s, err := Open(context.Background(), dirs, Endpoints{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	data, err := os.ReadFile(dirs.ConfigFile())
	if err != nil || !strings.Contains(string(data), "library_path:") {
		t.Fatalf("config not written: %v\n%s", err, data)
	}
	warn := strings.Join(s.Deps.Warnings, "\n")
	if !strings.Contains(warn, "wrote a default config to "+dirs.ConfigFile()) || !strings.Contains(warn, "discogs: no token") {
		t.Errorf("warnings = %q", warn)
	}
	if s.Deps.Lib == nil || s.Deps.Queue == nil || s.Deps.Sources == nil || s.Deps.ConfigPath != dirs.ConfigFile() {
		t.Errorf("deps = %+v", s.Deps)
	}
	// Discogs is skipped without a token, MusicBrainz remains
	if id := s.Deps.Identifier; id == nil || len(id.Providers) != 1 || id.Providers[0].Name() != "musicbrainz" {
		t.Errorf("identifier = %+v", id)
	}
	if s.Deps.DiscogsToken != secrets.SourceNone {
		t.Errorf("token source = %q", s.Deps.DiscogsToken)
	}
}

func TestOpenUsesTheExistingConfigAndToken(t *testing.T) {
	secrets.UseMockKeychain()
	t.Setenv(secrets.DiscogsToken.Env, "tok")
	dirs := testDirs(t)
	lib := t.TempDir()
	body := "# mine\nlibrary_path: " + lib + "\nactive_preset: full\nproviders: [discogs]\n"
	if err := os.MkdirAll(dirs.Config, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirs.ConfigFile(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(context.Background(), dirs, Endpoints{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Deps.Config.LibraryPath != lib || s.Deps.Lib.Root() != lib {
		t.Errorf("library = %q / %q", s.Deps.Config.LibraryPath, s.Deps.Lib.Root())
	}
	if strings.Join(s.Deps.Config.Removed, ",") != "active_preset" {
		t.Errorf("Removed = %v", s.Deps.Config.Removed)
	}
	if strings.Contains(strings.Join(s.Deps.Warnings, "\n"), "default config") {
		t.Error("an existing config must not be replaced")
	}
	if id := s.Deps.Identifier; id == nil || len(id.Providers) != 1 || id.Providers[0].Name() != "discogs" {
		t.Errorf("identifier = %+v", id)
	}
	if s.Deps.DiscogsToken != secrets.SourceEnv {
		t.Errorf("token source = %q", s.Deps.DiscogsToken)
	}
	if err := s.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func TestOpenWithoutProvidersStillWorks(t *testing.T) {
	secrets.UseMockKeychain()
	t.Setenv(secrets.DiscogsToken.Env, "")
	dirs := testDirs(t)
	os.MkdirAll(dirs.Config, 0o755)
	os.WriteFile(dirs.ConfigFile(), []byte("library_path: "+t.TempDir()+"\nproviders: [discogs]\n"), 0o644)

	s, err := Open(context.Background(), dirs, Endpoints{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.Deps.Identifier != nil || s.Deps.Images == nil {
		t.Errorf("identifier %v, images %v", s.Deps.Identifier, s.Deps.Images)
	}
	if !strings.Contains(strings.Join(s.Deps.Warnings, "\n"), "downloads keep the source's tags") {
		t.Errorf("warnings = %q", s.Deps.Warnings)
	}
}

func TestOpenNeverCreatesAConfiguredLibraryFolder(t *testing.T) {
	dirs := testDirs(t)
	missing := filepath.Join(t.TempDir(), "unmounted-drive", "music")
	os.MkdirAll(dirs.Config, 0o755)
	os.WriteFile(dirs.ConfigFile(), []byte("library_path: "+missing+"\n"), 0o644)
	if _, err := Open(context.Background(), dirs, Endpoints{}); err == nil {
		t.Fatal("a missing library folder should be an error")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("the folder was created")
	}
}

func TestOpenReportsABrokenConfig(t *testing.T) {
	dirs := testDirs(t)
	os.MkdirAll(dirs.Config, 0o755)
	os.WriteFile(dirs.ConfigFile(), []byte("queue:\n  parallel: 0\n"), 0o644)
	_, err := Open(context.Background(), dirs, Endpoints{})
	if err == nil || !strings.Contains(err.Error(), "queue.parallel") || !strings.Contains(err.Error(), dirs.ConfigFile()) {
		t.Errorf("err = %v", err)
	}
}
