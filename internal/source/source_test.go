package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"your-music-library/internal/testutil"
)

func fake(t *testing.T) *Plugin {
	t.Helper()
	plugins, err := Discover(testutil.FakeSourceDir(t))
	if err != nil || len(plugins) != 1 || plugins[0].Name != "fake" {
		t.Fatalf("Discover = %v, %v", plugins, err)
	}
	return plugins[0]
}

func TestPluginProtocol(t *testing.T) {
	p := fake(t)
	ctx := context.Background()

	info, err := p.Info(ctx)
	if err != nil || info.Name != "fake" || info.Version != "1.0" {
		t.Fatalf("Info = %+v, %v", info, err)
	}

	res, err := p.Search(ctx, "off the wall", KindAlbum, 10)
	if err != nil || len(res) != 1 || res[0].ID != "a1" || res[0].Kind != KindAlbum || res[0].Quality.String() != "FLAC 16/44.1" {
		t.Fatalf("Search = %+v, %v", res, err)
	}

	album, tracks, err := p.Resolve(ctx, "a1", KindAlbum)
	if err != nil || album.Title != "Off the Wall" || len(tracks) != 2 || tracks[0].ISRC != "USSM17900001" {
		t.Fatalf("Resolve = %+v %+v, %v", album, tracks, err)
	}
	_, tracks, err = p.Resolve(ctx, "s1", KindTrack)
	if err != nil || len(tracks) != 1 || tracks[0].ID != "s1" {
		t.Errorf("Resolve track = %+v, %v", tracks, err)
	}
	if _, _, err := p.Resolve(ctx, "nope", KindAlbum); err == nil || !strings.Contains(err.Error(), "unknown id nope") {
		t.Errorf("JSON error not reported: %v", err)
	}

	dest := t.TempDir()
	path, err := p.Download(ctx, "t1", dest)
	if err != nil || filepath.Dir(path) != dest || filepath.Base(path) != "t1.flac" {
		t.Fatalf("Download = %q, %v", path, err)
	}
	if _, err := p.Download(ctx, "a1", dest); err == nil || !strings.Contains(err.Error(), "cannot download a1") {
		t.Errorf("stderr not reported: %v", err)
	}
}

func TestDiscoverSkipsNonExecutables(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o755)
	os.WriteFile(filepath.Join(dir, "good.sh"), []byte("#!/bin/sh\n"), 0o755)
	plugins, err := Discover(dir)
	if err != nil || len(plugins) != 1 || plugins[0].Name != "good" {
		t.Errorf("Discover = %+v, %v", plugins, err)
	}
	if plugins, err := Discover(filepath.Join(dir, "missing")); err != nil || plugins != nil {
		t.Errorf("missing dir = %v, %v", plugins, err)
	}
}

func TestDownloadMustStayInDestDir(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "evil")
	os.WriteFile(script, []byte("#!/bin/sh\necho '{\"file\": \"../../etc/passwd\"}'\n"), 0o755)
	p := &Plugin{Name: "evil", Path: script}
	if _, err := p.Download(context.Background(), "x", t.TempDir()); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("escape not rejected: %v", err)
	}
}

func TestRefAndQuality(t *testing.T) {
	r, err := ParseRef("fake:a1")
	if err != nil || r.Source != "fake" || r.ID != "a1" || r.String() != "fake:a1" {
		t.Errorf("ParseRef = %+v, %v", r, err)
	}
	for _, bad := range []string{"", "fake", ":a1", "fake:"} {
		if _, err := ParseRef(bad); err == nil {
			t.Errorf("ParseRef(%q) accepted", bad)
		}
	}
	if got := (Quality{Format: "flac", BitDepth: 24, SampleRate: 96000}).String(); got != "FLAC 24/96" {
		t.Errorf("quality = %q", got)
	}
	if got := (Quality{Format: "mp3", BitrateKbps: 320}).String(); got != "MP3 320" {
		t.Errorf("quality = %q", got)
	}
}
