// Package app starts ymlib: it resolves the configuration (writing a
// default one on first start), opens the library, database, queue,
// plugins and metadata providers, and runs the TUI. cmd/ymlib only calls
// Run; tests call Open.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"your-music-library/internal/cache"
	"your-music-library/internal/config"
	"your-music-library/internal/library"
	"your-music-library/internal/metadata"
	"your-music-library/internal/platform"
	"your-music-library/internal/queue"
	"your-music-library/internal/secrets"
	"your-music-library/internal/source"
	"your-music-library/internal/store"
	"your-music-library/internal/termimg"
	"your-music-library/internal/tui"
)

// Version is sent in the User-Agent of metadata requests.
const Version = "0.1.0-dev"

// Endpoints override provider URLs (tests); empty means production.
type Endpoints struct {
	Discogs, MusicBrainz, CoverArt string
	MinInterval                    time.Duration // 0: each provider's own rate limit
}

// Session is what the UI works with. Close releases it.
type Session struct {
	Deps   tui.Deps
	closer []func() error // run in reverse order
}

// Close stops the lookup cache (saving it to SQLite) and closes the
// database.
func (s *Session) Close() error {
	var errs []error
	for i := len(s.closer) - 1; i >= 0; i-- {
		errs = append(errs, s.closer[i]())
	}
	return errors.Join(errs...)
}

// Run opens a session and runs the TUI, writing to out.
func Run(ctx context.Context, dirs platform.Dirs, out io.Writer) (err error) {
	s, err := Open(ctx, dirs, Endpoints{})
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	s.Deps.Covers = termimg.Detect(os.Getenv)
	s.Deps.Refresh = 2 * time.Second
	s.Deps.PluginRefresh = 15 * time.Second
	return tui.Run(ctx, s.Deps, out)
}

// Open prepares a session. A missing config file is created with the
// defaults, and so is the default library folder. Problems that leave ymlib usable (no metadata provider, for
// example) become Deps.Warnings, shown in the status line.
func Open(ctx context.Context, dirs platform.Dirs, ep Endpoints) (_ *Session, err error) {
	s := &Session{}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	path := dirs.ConfigFile()
	created, err := ensureConfig(path)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	d := tui.Deps{Config: cfg, ConfigPath: path, Sources: source.FromConfig(cfg.Plugins)}
	if created {
		d.Warnings = append(d.Warnings, "wrote a default config to "+path+"; set library_path there")
	}

	root, err := platform.ExpandHome(cfg.LibraryPath)
	if err != nil {
		return nil, err
	}
	if created {
		// first start: the default library folder. A configured folder is
		// never created, since it may be a drive that isn't mounted.
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := store.Open(dirs.DatabaseFile())
	if err != nil {
		return nil, err
	}
	s.closer = append(s.closer, db.Close)
	if d.Lib, err = library.Open(root, db, cfg.PathTemplate); err != nil {
		return nil, err
	}
	d.Queue = queue.New(db)

	maxAge := time.Duration(cfg.Cache.MaxAgeDays) * 24 * time.Hour
	files, err := cache.OpenFiles(filepath.Join(dirs.Cache, "covers"), maxAge)
	if err != nil {
		return nil, err
	}
	ua := userAgent(cfg)
	d.Images = metadata.NewImageFetcher(ua, files)

	lookups, err := cache.Open(ctx, db, maxAge)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	saved := make(chan error, 1)
	go func() { saved <- lookups.Run(runCtx, time.Minute) }()
	s.closer = append(s.closer, func() error {
		cancel()
		if err := <-saved; err != nil {
			return fmt.Errorf("saving the lookup cache: %w", err)
		}
		return nil
	})

	opts := metadata.HTTPOptions{UserAgent: ua, Cache: lookups, MinInterval: ep.MinInterval}
	var providers []metadata.Provider
	for _, name := range cfg.Providers {
		switch name {
		case "discogs":
			token, src, err := secrets.Get(secrets.DiscogsToken)
			d.DiscogsToken = src
			switch {
			case err != nil:
				d.Warnings = append(d.Warnings, fmt.Sprintf("discogs: reading the token: %v; skipping Discogs", err))
			case token == "":
				d.Warnings = append(d.Warnings, "discogs: no token (see the README); skipping Discogs")
			default:
				providers = append(providers, metadata.NewDiscogs(token, opts, ep.Discogs))
			}
		case "musicbrainz":
			providers = append(providers, metadata.NewMusicBrainz(opts, ep.MusicBrainz, ep.CoverArt))
		}
	}
	if len(providers) == 0 {
		d.Warnings = append(d.Warnings, "no metadata provider available; downloads keep the source's tags")
	} else {
		d.Identifier = &metadata.Identifier{Providers: providers, MinScore: cfg.Metadata.MinScore}
	}
	s.Deps = d
	return s, nil
}

// ensureConfig writes the default config to path unless a file exists.
func ensureConfig(path string) (created bool, err error) {
	if _, err := os.Stat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	out, err := config.Default().Marshal()
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	header := "# ymlib settings. Only the keys you set override the defaults; see the README.\n"
	return true, os.WriteFile(path, append([]byte(header), out...), 0o644)
}

func userAgent(cfg config.Config) string {
	ua := "ymlib/" + Version
	if c := strings.TrimSpace(cfg.Metadata.Contact); c != "" {
		ua += " ( " + c + " )"
	}
	return ua
}
