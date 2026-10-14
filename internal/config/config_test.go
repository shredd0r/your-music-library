package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadMissingFileGivesDefaults(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if c.ActivePreset != "full" {
		t.Errorf("ActivePreset = %q", c.ActivePreset)
	}
	if c.Singles != SinglesAsAlbum {
		t.Errorf("Singles = %q", c.Singles)
	}
	if c.Queue.Parallel != 2 {
		t.Errorf("Queue.Parallel = %d", c.Queue.Parallel)
	}
	if c.Cache.MaxAgeDays != 180 {
		t.Errorf("Cache.MaxAgeDays = %d", c.Cache.MaxAgeDays)
	}
	if got := strings.Join(c.Providers, ","); got != "discogs,musicbrainz" {
		t.Errorf("Providers = %q", got)
	}
	if c.PathTemplate != DefaultPathTemplate {
		t.Errorf("PathTemplate = %q", c.PathTemplate)
	}
	if c.Metadata.MinScore != 0.85 || c.Metadata.Contact != "" {
		t.Errorf("Metadata = %+v", c.Metadata)
	}
}

func TestBuiltinPresets(t *testing.T) {
	c := Default()
	full, ok := c.Preset("full")
	if !ok || len(full.Fields) != len(AllFields) || full.CoverMaxPx != 600 {
		t.Fatalf("full preset wrong: %+v", full)
	}
	mp3, ok := c.Preset("mp3-player")
	if !ok {
		t.Fatal("mp3-player missing")
	}
	if mp3.CoverMaxPx != 600 {
		t.Errorf("mp3-player cover = %d", mp3.CoverMaxPx)
	}
	if mp3.Has(FieldArtists) || mp3.Has(FieldPictureBack) {
		t.Errorf("mp3-player should not include Artists or Picture Back: %v", mp3.Fields)
	}
	for _, f := range []Field{FieldTitle, FieldAlbum, FieldArtist, FieldAlbumArtist, FieldDate, FieldTrackNumber, FieldDiscNumber, FieldGenre, FieldPictureCover} {
		if !mp3.Has(f) {
			t.Errorf("mp3-player missing %s", f)
		}
	}
}

func TestLoadUserConfig(t *testing.T) {
	p := writeFile(t, `
library_path: /music
active_preset: car
singles: grouped
queue:
  parallel: 4
cache:
  max_age_days: 30
providers: [musicbrainz]
presets:
  car:
    fields: [Title, Artist, "Track Number", "Picture Cover"]
    cover_max_px: 300
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.LibraryPath != "/music" || c.Singles != SinglesGrouped || c.Queue.Parallel != 4 || c.Cache.MaxAgeDays != 30 {
		t.Errorf("unexpected config: %+v", c)
	}
	car, ok := c.Preset("car")
	if !ok || len(car.Fields) != 4 || car.CoverMaxPx != 300 {
		t.Fatalf("car preset wrong: %+v", car)
	}
	if _, ok := c.Preset("full"); !ok {
		t.Error("built-in presets must remain available next to user presets")
	}
	// unset keys keep defaults
	if c.PathTemplate != DefaultPathTemplate {
		t.Errorf("PathTemplate = %q", c.PathTemplate)
	}
}

func TestUserPresetOverridesBuiltin(t *testing.T) {
	c, err := Load(writeFile(t, `
presets:
  mp3-player:
    fields: [Title]
`))
	if err != nil {
		t.Fatal(err)
	}
	p, _ := c.Preset("mp3-player")
	if len(p.Fields) != 1 || p.Builtin {
		t.Errorf("override not applied: %+v", p)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"unknown field":   "presets:\n  x:\n    fields: [Title, Mood]\n",
		"empty preset":    "presets:\n  x:\n    fields: []\n",
		"unknown active":  "active_preset: nope\n",
		"bad singles":     "singles: sometimes\n",
		"bad parallel":    "queue:\n  parallel: 0\n",
		"bad cache age":   "cache:\n  max_age_days: -1\n",
		"bad min score":   "metadata:\n  min_score: 1.5\n",
		"zero min score":  "metadata:\n  min_score: 0\n",
		"bad provider":    "providers: [discogs, napster]\n",
		"no providers":    "providers: []\n",
		"negative cover":  "presets:\n  x:\n    fields: [Title]\n    cover_max_px: -5\n",
		"empty template":  "path_template: \"\"\n",
		"bad template":    "path_template: \"{album}/{nope}.{ext}\"\n",
		"unknown key":     "libary_path: /typo\n",
		"duplicate field": "presets:\n  x:\n    fields: [Title, Title]\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeFile(t, body)); err == nil {
				t.Errorf("expected error for %s", name)
			}
		})
	}
}

func TestParseFieldIsCaseAndSpaceInsensitive(t *testing.T) {
	for _, s := range []string{"Track Number", "track number", "TRACK_NUMBER", "track-number", "tracknumber"} {
		f, err := ParseField(s)
		if err != nil || f != FieldTrackNumber {
			t.Errorf("ParseField(%q) = %v, %v", s, f, err)
		}
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	c := Default()
	c.LibraryPath = "/music"
	out, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Load(writeFile(t, string(out)))
	if err != nil {
		t.Fatalf("reloading marshalled config: %v\n%s", err, out)
	}
	if c2.LibraryPath != "/music" || c2.ActivePreset != c.ActivePreset {
		t.Errorf("round trip lost data: %+v", c2)
	}
}
