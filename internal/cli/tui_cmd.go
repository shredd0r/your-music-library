package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"your-music-library/internal/cache"
	"your-music-library/internal/cleanup"
	"your-music-library/internal/metadata"
	"your-music-library/internal/queue"
	"your-music-library/internal/termimg"
	"your-music-library/internal/tui"
)

func newTUICommand(a *app, loadConfig preRun) *cobra.Command {
	return &cobra.Command{
		Use:     "tui",
		Short:   "Open the terminal UI (also: ymlib without arguments)",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE:    func(cmd *cobra.Command, _ []string) error { return a.runTUI(cmd) },
	}
}

// interactive reports whether stdin and stdout are terminals.
func interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// lockedBuffer collects warnings written by background work.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(b.buf.String()), "\n") {
		if l = strings.TrimSpace(strings.TrimPrefix(l, "warning: ")); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (a *app) runTUI(cmd *cobra.Command) (err error) {
	lib, db, err := a.openLibrary()
	if err != nil {
		return err
	}
	defer cleanup.Close(&err, db)
	reg, err := a.registry()
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	d := tui.Deps{
		Lib: lib, Queue: queue.New(db), Sources: reg,
		Config: a.cfg, ConfigPath: a.configPath, SourcesDir: a.sourcesDir(),
		Covers:  termimg.Detect(os.Getenv),
		Refresh: 2 * time.Second,
	}
	// Warnings are shown inside the UI; anything printed to the terminal
	// would be drawn over.
	var warn lockedBuffer
	ms, err := a.openMetadata(ctx, db, &warn)
	if err != nil {
		d.Warnings = append(d.Warnings, err.Error()+"; downloads keep the source's tags")
	} else {
		defer ms.Close()
		d.Identifier, d.Images = ms.identifier, ms.images
	}
	if d.Images == nil {
		maxAge := time.Duration(a.cfg.Cache.MaxAgeDays) * 24 * time.Hour
		files, err := cache.OpenFiles(filepath.Join(a.dirs.Cache, "covers"), maxAge)
		if err != nil {
			return err
		}
		d.Images = metadata.NewImageFetcher(a.userAgent(), files)
	}
	d.Warnings = append(warn.lines(), d.Warnings...)
	return tui.Run(ctx, d, cmd.OutOrStdout())
}
