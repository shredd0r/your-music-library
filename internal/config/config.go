// Package config loads and validates config.yaml: library location, path
// template, metadata providers, queue, cache and plugin settings.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"your-music-library/internal/pathtmpl"
)

// DefaultPathTemplate places files under the album artist folder.
// Template syntax is defined by package pathtmpl.
const DefaultPathTemplate = "{albumartist}/{album}/{disc?}{track:02} - {title}.{ext}"

// KnownProviders are the metadata providers this build supports.
var KnownProviders = []string{"discogs", "musicbrainz"}

type QueueConfig struct {
	Parallel int `yaml:"parallel"`
}

// MetadataConfig controls metadata lookups (Discogs, MusicBrainz).
type MetadataConfig struct {
	// MinScore (0..1): matches scoring at least this are applied
	// automatically; weaker ones go to the Needs Review list.
	MinScore float64 `yaml:"min_score"`
	// Contact (URL or email) is added to the User-Agent that MusicBrainz
	// requires. Optional; nothing personal is sent unless set here.
	Contact string `yaml:"contact,omitempty"`
}

type CacheConfig struct {
	// MaxAgeDays: cache entries older than this are purged on startup.
	MaxAgeDays int `yaml:"max_age_days"`
}

// PluginsConfig lists the source plugins: HTTP servers the user runs.
type PluginsConfig struct {
	// OfflineWaitMinutes: queued downloads of an offline or disabled
	// plugin wait this long, then fail.
	OfflineWaitMinutes int `yaml:"offline_wait_minutes"`
	// Registered maps a plugin name (used in references, name:id) to its
	// server.
	Registered map[string]PluginEntry `yaml:"registered,omitempty"`
}

// PluginEntry is one registered plugin.
type PluginEntry struct {
	URL     string `yaml:"url"`
	Enabled bool   `yaml:"enabled"`
}

// ValidatePlugin checks a plugin name and URL. Names are used in
// references (name:id), so they can't contain ':' or spaces.
func ValidatePlugin(name, rawURL string) error {
	if name == "" || strings.ContainsAny(name, ": \t/") {
		return fmt.Errorf("plugin name %q must be non-empty without ':', '/' or spaces", name)
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("plugin %s: url %q must be http(s)://host:port", name, rawURL)
	}
	return nil
}

// Config is the effective configuration: defaults merged with the user file.
type Config struct {
	LibraryPath  string
	PathTemplate string
	Providers    []string
	Queue        QueueConfig
	Cache        CacheConfig
	Metadata     MetadataConfig
	Plugins      PluginsConfig

	// Removed lists deprecated keys that Load deleted from the file
	// (see deprecatedKeys), so the UI can say so once. Usually nil.
	Removed []string
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		LibraryPath:  "~/Music/ymlib",
		PathTemplate: DefaultPathTemplate,
		Providers:    slices.Clone(KnownProviders),
		Queue:        QueueConfig{Parallel: 2},
		Cache:        CacheConfig{MaxAgeDays: 180},
		Metadata:     MetadataConfig{MinScore: 0.85},
		Plugins:      PluginsConfig{OfflineWaitMinutes: 60},
	}
}

// fileConfig mirrors config.yaml. Pointers distinguish "unset" from zero.
type fileConfig struct {
	LibraryPath  *string         `yaml:"library_path,omitempty"`
	PathTemplate *string         `yaml:"path_template,omitempty"`
	Providers    *[]string       `yaml:"providers,omitempty"`
	Queue        *QueueConfig    `yaml:"queue,omitempty"`
	Cache        *CacheConfig    `yaml:"cache,omitempty"`
	Metadata     *MetadataConfig `yaml:"metadata,omitempty"`
	Plugins      *filePlugins    `yaml:"plugins,omitempty"`
}

// filePlugins merges key by key, so setting one key keeps the other's default.
type filePlugins struct {
	OfflineWaitMinutes *int                   `yaml:"offline_wait_minutes,omitempty"`
	Registered         map[string]PluginEntry `yaml:"registered,omitempty"`
}

// Load reads path and merges it over Default. A missing file is not an
// error: the defaults are returned. Deprecated keys are deleted from the
// file (comments and other settings are kept) and listed in Removed.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	if removed := deprecatedIn(&doc); len(removed) > 0 {
		c, err := editFile(path, func(root *yaml.Node) error {
			dropKeys(root, removed)
			return nil
		})
		c.Removed = removed
		return c, err
	}
	c, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// deprecatedKeys were settings of earlier versions that no longer mean
// anything: presets (the tag format is fixed) and the singles mode.
// Parse ignores them and Load deletes them from the file.
var deprecatedKeys = []string{"active_preset", "singles", "presets"}

// deprecatedIn lists the deprecated top-level keys in doc, in file order.
func deprecatedIn(doc *yaml.Node) []string {
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	var out []string
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if slices.Contains(deprecatedKeys, m.Content[i].Value) {
			out = append(out, m.Content[i].Value)
		}
	}
	return out
}

// dropKeys removes keys from mapping m. The comment above the first key
// is usually the file's header, so it moves to the next key that stays.
func dropKeys(m *yaml.Node, keys []string) {
	var header string
	for i := 0; i+1 < len(m.Content); {
		k := m.Content[i]
		if slices.Contains(keys, k.Value) {
			if i == 0 && header == "" {
				header = k.HeadComment
			}
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			continue
		}
		if i == 0 && header != "" {
			k.HeadComment = strings.TrimSpace(header + "\n" + k.HeadComment)
		}
		i += 2
	}
}

// removedKeys were settings of earlier versions; they get a hint instead
// of the plain "unknown field" error.
var removedKeys = map[string]string{
	"sources_dir": "sources_dir was removed: plugins are now servers you run; add them in Settings (a)",
	"sources":     "the sources section was removed: plugins are now servers you run; add them in Settings (a)",
}

// Parse merges YAML data over Default and validates the result.
func Parse(data []byte) (Config, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return Config{}, err
	}
	if len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
		m := doc.Content[0]
		for i := 0; i+1 < len(m.Content); i += 2 {
			if hint, ok := removedKeys[m.Content[i].Value]; ok {
				return Config{}, fmt.Errorf("line %d: %s", m.Content[i].Line, hint)
			}
		}
		if old := deprecatedIn(&doc); len(old) > 0 {
			dropKeys(m, old)
			var err error
			if data, err = yaml.Marshal(&doc); err != nil {
				return Config{}, err
			}
		}
	}
	var fc fileConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&fc); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, err
	}

	c := Default()
	if fc.LibraryPath != nil {
		c.LibraryPath = *fc.LibraryPath
	}
	if fc.PathTemplate != nil {
		c.PathTemplate = *fc.PathTemplate
	}
	if fc.Providers != nil {
		c.Providers = *fc.Providers
	}
	if fc.Queue != nil {
		c.Queue = *fc.Queue
	}
	if fc.Cache != nil {
		c.Cache = *fc.Cache
	}
	if fc.Metadata != nil {
		c.Metadata = *fc.Metadata
	}
	if fc.Plugins != nil {
		if fc.Plugins.OfflineWaitMinutes != nil {
			c.Plugins.OfflineWaitMinutes = *fc.Plugins.OfflineWaitMinutes
		}
		c.Plugins.Registered = fc.Plugins.Registered
	}
	return c, c.Validate()
}

// Validate checks cross-field rules.
func (c Config) Validate() error {
	var errs []error
	if c.LibraryPath == "" {
		errs = append(errs, errors.New("library_path must not be empty"))
	}
	if _, err := pathtmpl.Parse(c.PathTemplate); err != nil {
		errs = append(errs, err)
	}
	if len(c.Providers) == 0 {
		errs = append(errs, errors.New("providers must list at least one provider"))
	}
	for _, p := range c.Providers {
		if !slices.Contains(KnownProviders, p) {
			errs = append(errs, fmt.Errorf("unknown provider %q (known: %v)", p, KnownProviders))
		}
	}
	if c.Queue.Parallel < 1 {
		errs = append(errs, errors.New("queue.parallel must be >= 1"))
	}
	if c.Metadata.MinScore <= 0 || c.Metadata.MinScore > 1 {
		errs = append(errs, errors.New("metadata.min_score must be in (0, 1]"))
	}
	if c.Cache.MaxAgeDays < 1 {
		errs = append(errs, errors.New("cache.max_age_days must be >= 1"))
	}
	if c.Plugins.OfflineWaitMinutes < 1 {
		errs = append(errs, errors.New("plugins.offline_wait_minutes must be >= 1"))
	}
	for _, name := range slices.Sorted(maps.Keys(c.Plugins.Registered)) {
		if err := ValidatePlugin(name, c.Plugins.Registered[name].URL); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Marshal renders the config as YAML.
func (c Config) Marshal() ([]byte, error) {
	fc := fileConfig{
		LibraryPath:  &c.LibraryPath,
		PathTemplate: &c.PathTemplate,
		Providers:    &c.Providers,
		Queue:        &c.Queue,
		Cache:        &c.Cache,
		Metadata:     &c.Metadata,
		Plugins:      &filePlugins{OfflineWaitMinutes: &c.Plugins.OfflineWaitMinutes, Registered: c.Plugins.Registered},
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fc); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}
