// Package platform hides OS differences (directories now; volumes later)
// so a Windows implementation can be added without touching callers.
package platform

import (
	"os"
	"path/filepath"
)

const appName = "ymlib"

// Dirs are the per-user directories the app reads and writes.
type Dirs struct {
	Config string // config.yaml lives here
	Cache  string // cover images and other disposable files
	Data   string // SQLite database
}

// ConfigFile is the default config path inside Dirs.Config.
func (d Dirs) ConfigFile() string { return filepath.Join(d.Config, "config.yaml") }

// DatabaseFile is the default SQLite path inside Dirs.Data.
func (d Dirs) DatabaseFile() string { return filepath.Join(d.Data, "library.db") }

// UserDirs returns XDG-style directories on macOS and Linux:
// $XDG_CONFIG_HOME or ~/.config, $XDG_CACHE_HOME or ~/.cache,
// $XDG_DATA_HOME or ~/.local/share, each with an "ymlib" subfolder.
func UserDirs() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	return dirsFrom(home, os.Getenv), nil
}

func dirsFrom(home string, getenv func(string) string) Dirs {
	base := func(env string, fallback ...string) string {
		if v := getenv(env); v != "" && filepath.IsAbs(v) {
			return filepath.Join(v, appName)
		}
		return filepath.Join(append([]string{home}, append(fallback, appName)...)...)
	}
	return Dirs{
		Config: base("XDG_CONFIG_HOME", ".config"),
		Cache:  base("XDG_CACHE_HOME", ".cache"),
		Data:   base("XDG_DATA_HOME", ".local", "share"),
	}
}

// ExpandHome replaces a leading "~" with the user's home directory.
func ExpandHome(path string) (string, error) {
	if path != "~" && !hasHomePrefix(path) {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, path[1:]), nil
}

func hasHomePrefix(p string) bool {
	return len(p) >= 2 && p[0] == '~' && (p[1] == '/' || p[1] == filepath.Separator)
}
