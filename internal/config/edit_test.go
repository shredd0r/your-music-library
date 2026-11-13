package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	orig := "# my settings\nlibrary_path: ~/Music/lib # where music lives\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := AddPlugin(path, "shelf", "http://127.0.0.1:8765")
	if err != nil || c.Plugins.Registered["shelf"] != (PluginEntry{URL: "http://127.0.0.1:8765", Enabled: true}) {
		t.Fatalf("AddPlugin = %+v, %v", c.Plugins, err)
	}
	if _, err := AddPlugin(path, "other", "http://nas:9000"); err != nil {
		t.Fatal(err)
	}
	if _, err := AddPlugin(path, "shelf", "http://127.0.0.1:1"); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Errorf("duplicate add: %v", err)
	}
	if _, err := AddPlugin(path, "bad name", "http://h:1"); err == nil {
		t.Error("bad name accepted")
	}
	if _, err := AddPlugin(path, "x", "localhost:8765"); err == nil {
		t.Error("url without scheme accepted")
	}

	c, err = SetPluginEnabled(path, "shelf", false)
	if err != nil || c.Plugins.Registered["shelf"].Enabled {
		t.Errorf("disable = %+v, %v", c.Plugins.Registered, err)
	}
	if _, err := SetPluginEnabled(path, "nope", true); err == nil {
		t.Error("enabling an unknown plugin")
	}
	c, err = RemovePlugin(path, "other")
	if _, still := c.Plugins.Registered["other"]; err != nil || still || len(c.Plugins.Registered) != 1 {
		t.Errorf("remove = %+v, %v", c.Plugins.Registered, err)
	}
	if _, err := RemovePlugin(path, "other"); err == nil {
		t.Error("removing twice")
	}

	data, _ := os.ReadFile(path)
	for _, want := range []string{"# my settings", "# where music lives", "enabled: false"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("file lost %q:\n%s", want, data)
		}
	}
	if c, err := Load(path); err != nil || c.Plugins.Registered["shelf"].URL != "http://127.0.0.1:8765" {
		t.Errorf("reload = %+v, %v", c.Plugins, err)
	}
}

func TestAddPluginCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	if _, err := AddPlugin(path, "shelf", "http://127.0.0.1:8765"); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(path); err != nil || len(c.Plugins.Registered) != 1 {
		t.Errorf("reload = %+v, %v", c.Plugins, err)
	}
}
