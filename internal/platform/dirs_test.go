package platform

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirsFromDefaults(t *testing.T) {
	d := dirsFrom("/home/u", func(string) string { return "" })
	want := Dirs{
		Config: "/home/u/.config/ymlib",
		Cache:  "/home/u/.cache/ymlib",
		Data:   "/home/u/.local/share/ymlib",
	}
	if d != want {
		t.Fatalf("got %+v, want %+v", d, want)
	}
	if got := d.ConfigFile(); got != "/home/u/.config/ymlib/config.yaml" {
		t.Errorf("ConfigFile = %q", got)
	}
	if got := d.DatabaseFile(); got != "/home/u/.local/share/ymlib/library.db" {
		t.Errorf("DatabaseFile = %q", got)
	}
}

func TestDirsFromXDG(t *testing.T) {
	env := map[string]string{
		"XDG_CONFIG_HOME": "/xdg/config",
		"XDG_CACHE_HOME":  "relative/ignored", // spec: relative paths must be ignored
	}
	d := dirsFrom("/home/u", func(k string) string { return env[k] })
	if d.Config != "/xdg/config/ymlib" {
		t.Errorf("Config = %q", d.Config)
	}
	if d.Cache != "/home/u/.cache/ymlib" {
		t.Errorf("Cache = %q", d.Cache)
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := map[string]string{
		"~":           home,
		"~/Music":     filepath.Join(home, "Music"),
		"/abs/path":   "/abs/path",
		"~user/Music": "~user/Music", // other users' homes are not expanded
		"rel":         "rel",
	}
	for in, want := range cases {
		got, err := ExpandHome(in)
		if err != nil || got != want {
			t.Errorf("ExpandHome(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}
