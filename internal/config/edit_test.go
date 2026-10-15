package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetActivePresetKeepsComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := `# my settings
library_path: ~/Music/lib   # where music lives
active_preset: full
presets:
  car:
    fields: [Title, Artist]
`
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := SetActivePreset(path, "car")
	if err != nil {
		t.Fatal(err)
	}
	if c.ActivePreset != "car" {
		t.Errorf("returned config has %q", c.ActivePreset)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	for _, want := range []string{"# my settings", "# where music lives", "active_preset: car", "fields: [Title, Artist]"} {
		if !strings.Contains(got, want) {
			t.Errorf("file lost %q:\n%s", want, got)
		}
	}
	if c2, err := Load(path); err != nil || c2.ActivePreset != "car" {
		t.Errorf("reload = %q, %v", c2.ActivePreset, err)
	}
}

func TestSetActivePresetAddsKeyAndCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("singles: grouped\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetActivePreset(path, "mp3-player"); err != nil {
		t.Fatal(err)
	}
	if c, _ := Load(path); c.ActivePreset != "mp3-player" || c.Singles != SinglesGrouped {
		t.Errorf("got %q %q", c.ActivePreset, c.Singles)
	}

	missing := filepath.Join(dir, "sub", "config.yaml")
	if _, err := SetActivePreset(missing, "mp3-player"); err != nil {
		t.Fatal(err)
	}
	if c, _ := Load(missing); c.ActivePreset != "mp3-player" {
		t.Errorf("created file has %q", c.ActivePreset)
	}
}

func TestSetActivePresetRejectsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("active_preset: full\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := SetActivePreset(path, "nope"); err == nil {
		t.Fatal("unknown preset accepted")
	}
	if data, _ := os.ReadFile(path); string(data) != "active_preset: full\n" {
		t.Errorf("file changed: %q", data)
	}
}
