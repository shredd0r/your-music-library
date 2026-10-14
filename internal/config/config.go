// Package config loads and validates config.yaml: library location,
// metadata presets, path template, singles mode, queue and cache settings.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"

	"gopkg.in/yaml.v3"

	"your-music-library/internal/pathtmpl"
)

// DefaultPathTemplate places files under the album artist folder.
// Template syntax is defined by package pathtmpl.
const DefaultPathTemplate = "{albumartist}/{album}/{disc?}{track:02} - {title}.{ext}"

// SinglesMode controls how single releases are stored.
type SinglesMode string

const (
	// SinglesAsAlbum stores each single as its own one-track album.
	SinglesAsAlbum SinglesMode = "as-album"
	// SinglesGrouped collects an artist's singles into "<Artist> Singles",
	// numbered by release date.
	SinglesGrouped SinglesMode = "grouped"
)

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

// Config is the effective configuration: defaults merged with the user file.
type Config struct {
	LibraryPath  string
	ActivePreset string
	PathTemplate string
	Singles      SinglesMode
	Providers    []string
	Queue        QueueConfig
	Cache        CacheConfig
	Metadata     MetadataConfig
	// Presets holds built-in and user presets; user presets with the same
	// name replace built-ins.
	Presets map[string]Preset
}

// Default returns the configuration used when no file exists.
func Default() Config {
	return Config{
		LibraryPath:  "~/Music/ymlib",
		ActivePreset: "full",
		PathTemplate: DefaultPathTemplate,
		Singles:      SinglesAsAlbum,
		Providers:    slices.Clone(KnownProviders),
		Queue:        QueueConfig{Parallel: 2},
		Cache:        CacheConfig{MaxAgeDays: 180},
		Metadata:     MetadataConfig{MinScore: 0.85},
		Presets:      builtinPresets(),
	}
}

// Preset looks up a preset by name.
func (c Config) Preset(name string) (Preset, bool) {
	p, ok := c.Presets[name]
	return p, ok
}

// PresetNames returns preset names sorted alphabetically.
func (c Config) PresetNames() []string {
	names := make([]string, 0, len(c.Presets))
	for n := range c.Presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// fileConfig mirrors config.yaml. Pointers distinguish "unset" from zero.
type fileConfig struct {
	LibraryPath  *string               `yaml:"library_path,omitempty"`
	ActivePreset *string               `yaml:"active_preset,omitempty"`
	PathTemplate *string               `yaml:"path_template,omitempty"`
	Singles      *string               `yaml:"singles,omitempty"`
	Providers    *[]string             `yaml:"providers,omitempty"`
	Queue        *QueueConfig          `yaml:"queue,omitempty"`
	Cache        *CacheConfig          `yaml:"cache,omitempty"`
	Metadata     *MetadataConfig       `yaml:"metadata,omitempty"`
	Presets      map[string]filePreset `yaml:"presets,omitempty"`
}

type filePreset struct {
	Fields     []string `yaml:"fields"`
	CoverMaxPx int      `yaml:"cover_max_px,omitempty"`
}

// Load reads path and merges it over Default. A missing file is not an
// error: the defaults are returned.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	c, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Parse merges YAML data over Default and validates the result.
func Parse(data []byte) (Config, error) {
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
	if fc.ActivePreset != nil {
		c.ActivePreset = *fc.ActivePreset
	}
	if fc.PathTemplate != nil {
		c.PathTemplate = *fc.PathTemplate
	}
	if fc.Singles != nil {
		c.Singles = SinglesMode(*fc.Singles)
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
	for name, fp := range fc.Presets {
		p, err := fp.toPreset(name)
		if err != nil {
			return Config{}, err
		}
		c.Presets[name] = p
	}
	return c, c.Validate()
}

func (fp filePreset) toPreset(name string) (Preset, error) {
	if len(fp.Fields) == 0 {
		return Preset{}, fmt.Errorf("preset %q: no fields", name)
	}
	if fp.CoverMaxPx < 0 {
		return Preset{}, fmt.Errorf("preset %q: cover_max_px must be >= 0", name)
	}
	p := Preset{Name: name, CoverMaxPx: fp.CoverMaxPx}
	for _, s := range fp.Fields {
		f, err := ParseField(s)
		if err != nil {
			return Preset{}, fmt.Errorf("preset %q: %w", name, err)
		}
		if p.Has(f) {
			return Preset{}, fmt.Errorf("preset %q: field %q listed twice", name, f)
		}
		p.Fields = append(p.Fields, f)
	}
	return p, nil
}

// Validate checks cross-field rules.
func (c Config) Validate() error {
	var errs []error
	if c.LibraryPath == "" {
		errs = append(errs, errors.New("library_path must not be empty"))
	}
	if _, ok := c.Presets[c.ActivePreset]; !ok {
		errs = append(errs, fmt.Errorf("active_preset %q does not exist", c.ActivePreset))
	}
	if _, err := pathtmpl.Parse(c.PathTemplate); err != nil {
		errs = append(errs, err)
	}
	if c.Singles != SinglesAsAlbum && c.Singles != SinglesGrouped {
		errs = append(errs, fmt.Errorf("singles must be %q or %q, got %q", SinglesAsAlbum, SinglesGrouped, c.Singles))
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
	return errors.Join(errs...)
}

// Marshal renders the config as YAML. Built-in presets are omitted unless
// the user overrode them, so the output stays short and editable.
func (c Config) Marshal() ([]byte, error) {
	singles := string(c.Singles)
	fc := fileConfig{
		LibraryPath:  &c.LibraryPath,
		ActivePreset: &c.ActivePreset,
		PathTemplate: &c.PathTemplate,
		Singles:      &singles,
		Providers:    &c.Providers,
		Queue:        &c.Queue,
		Cache:        &c.Cache,
		Metadata:     &c.Metadata,
	}
	for name, p := range c.Presets {
		if p.Builtin {
			continue
		}
		if fc.Presets == nil {
			fc.Presets = map[string]filePreset{}
		}
		fp := filePreset{CoverMaxPx: p.CoverMaxPx}
		for _, f := range p.Fields {
			fp.Fields = append(fp.Fields, string(f))
		}
		fc.Presets[name] = fp
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(fc); err != nil {
		return nil, err
	}
	return buf.Bytes(), enc.Close()
}
