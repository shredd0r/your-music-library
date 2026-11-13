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

func TestLoadUserConfig(t *testing.T) {
	p := writeFile(t, `
library_path: /music
queue:
  parallel: 4
cache:
  max_age_days: 30
providers: [musicbrainz]
plugins:
  registered:
    shelf: {url: "http://127.0.0.1:8765", enabled: true}
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.LibraryPath != "/music" || c.Queue.Parallel != 4 || c.Cache.MaxAgeDays != 30 {
		t.Errorf("unexpected config: %+v", c)
	}
	// one key of a section set: the other keeps its default
	if c.Plugins.OfflineWaitMinutes != 60 || c.Plugins.Registered["shelf"] != (PluginEntry{URL: "http://127.0.0.1:8765", Enabled: true}) {
		t.Errorf("Plugins = %+v", c.Plugins)
	}
	// unset keys keep defaults
	if c.PathTemplate != DefaultPathTemplate {
		t.Errorf("PathTemplate = %q", c.PathTemplate)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"bad parallel":     "queue:\n  parallel: 0\n",
		"bad cache age":    "cache:\n  max_age_days: -1\n",
		"bad offline wait": "plugins:\n  offline_wait_minutes: 0\n",
		"plugin url":       "plugins:\n  registered:\n    x: {url: \"ftp://h\"}\n",
		"plugin no host":   "plugins:\n  registered:\n    x: {url: \"http://\"}\n",
		"plugin name":      "plugins:\n  registered:\n    \"a:b\": {url: \"http://h:1\"}\n",
		"plugin key":       "plugins:\n  registered:\n    x: {url: \"http://h:1\", port: 3}\n",
		"bad min score":    "metadata:\n  min_score: 1.5\n",
		"zero min score":   "metadata:\n  min_score: 0\n",
		"bad provider":     "providers: [discogs, napster]\n",
		"no providers":     "providers: []\n",
		"empty template":   "path_template: \"\"\n",
		"bad template":     "path_template: \"{album}/{nope}.{ext}\"\n",
		"unknown key":      "libary_path: /typo\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeFile(t, body)); err == nil {
				t.Errorf("expected error for %s", name)
			}
		})
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
	if c2.LibraryPath != "/music" || c2.PathTemplate != c.PathTemplate {
		t.Errorf("round trip lost data: %+v", c2)
	}
}

func TestRemovedKeysHint(t *testing.T) {
	for _, data := range []string{"sources_dir: /x\n", "library_path: /m\nsources:\n  idle_minutes: 3\n"} {
		_, err := Parse([]byte(data))
		if err == nil || !strings.Contains(err.Error(), "ymlib plugin add") {
			t.Errorf("%q: %v", data, err)
		}
	}
}

func TestLoadRemovesDeprecatedKeys(t *testing.T) {
	p := writeFile(t, `# my settings
library_path: /music # where music lives
active_preset: car
singles: grouped
presets:
  car:
    fields: [Title, Artist]
queue:
  parallel: 4
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"active_preset", "singles", "presets"}; strings.Join(c.Removed, ",") != strings.Join(want, ",") {
		t.Errorf("Removed = %v, want %v", c.Removed, want)
	}
	if c.LibraryPath != "/music" || c.Queue.Parallel != 4 {
		t.Errorf("other settings lost: %+v", c)
	}
	data, _ := os.ReadFile(p)
	for _, gone := range []string{"active_preset", "singles", "presets", "car", "fields"} {
		if strings.Contains(string(data), gone) {
			t.Errorf("file still has %q:\n%s", gone, data)
		}
	}
	for _, kept := range []string{"# my settings", "# where music lives", "parallel: 4"} {
		if !strings.Contains(string(data), kept) {
			t.Errorf("file lost %q:\n%s", kept, data)
		}
	}
	if c2, err := Load(p); err != nil || c2.Removed != nil {
		t.Errorf("second load: %v, Removed = %v", err, c2.Removed)
	}
}

func TestLoadKeepsHeaderAboveRemovedKey(t *testing.T) {
	p := writeFile(t, "# ymlib settings\nactive_preset: full\n# where music lives\nlibrary_path: /music\n")
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(data), "# ymlib settings\n# where music lives\nlibrary_path: /music") {
		t.Errorf("file:\n%s", data)
	}
}

func TestParseIgnoresDeprecatedKeys(t *testing.T) {
	c, err := Parse([]byte("active_preset: nope\nsingles: sometimes\npresets: {x: {fields: [Mood]}}\n"))
	if err != nil || c.Removed != nil {
		t.Errorf("Parse = %+v, %v", c, err)
	}
}
