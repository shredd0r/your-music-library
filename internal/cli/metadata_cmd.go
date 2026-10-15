package cli

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"your-music-library/internal/cache"
	"your-music-library/internal/cleanup"
	"your-music-library/internal/library"
	"your-music-library/internal/metadata"
	"your-music-library/internal/secrets"
)

// endpoints override provider URLs (tests); empty means production.
type endpoints struct {
	discogs, musicbrainz, coverArt string
	minInterval                    time.Duration // 0: each provider's own rate limit
}

func (a *app) userAgent() string {
	ua := "ymlib/" + version
	if c := strings.TrimSpace(a.cfg.Metadata.Contact); c != "" {
		ua += " ( " + c + " )"
	}
	return ua
}

// metadataSession bundles what metadata commands need; Close flushes the
// cache to SQLite.
type metadataSession struct {
	identifier *metadata.Identifier
	images     *metadata.ImageFetcher
	stop       func()
}

func (s *metadataSession) Close() { s.stop() }

func (a *app) openMetadata(ctx context.Context, db *sql.DB, warn io.Writer) (*metadataSession, error) {
	maxAge := time.Duration(a.cfg.Cache.MaxAgeDays) * 24 * time.Hour
	store, err := cache.Open(ctx, db, maxAge)
	if err != nil {
		return nil, err
	}
	files, err := cache.OpenFiles(filepath.Join(a.dirs.Cache, "covers"), maxAge)
	if err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		if err := store.Run(runCtx, time.Minute); err != nil {
			fmt.Fprintf(warn, "warning: saving the lookup cache: %v\n", err)
		}
		close(done)
	}()
	stop := func() { cancel(); <-done }

	opts := metadata.HTTPOptions{UserAgent: a.userAgent(), Cache: store, MinInterval: a.endpoints.minInterval}
	var providers []metadata.Provider
	for _, name := range a.cfg.Providers {
		switch name {
		case "discogs":
			token, _, err := secrets.Get(secrets.DiscogsToken)
			if err != nil {
				fmt.Fprintf(warn, "warning: discogs: reading token: %v; skipping Discogs\n", err)
				continue
			}
			if token == "" {
				fmt.Fprintln(warn, "warning: discogs: no token (run `ymlib auth discogs`); skipping Discogs")
				continue
			}
			providers = append(providers, metadata.NewDiscogs(token, opts, a.endpoints.discogs))
		case "musicbrainz":
			providers = append(providers, metadata.NewMusicBrainz(opts, a.endpoints.musicbrainz, a.endpoints.coverArt))
		}
	}
	if len(providers) == 0 {
		stop()
		return nil, errors.New("no metadata provider available")
	}
	return &metadataSession{
		identifier: &metadata.Identifier{Providers: providers, MinScore: a.cfg.Metadata.MinScore},
		images:     metadata.NewImageFetcher(a.userAgent(), files),
		stop:       stop,
	}, nil
}

func newMetadataCommands(a *app, loadConfig preRun) []*cobra.Command {
	var force, apply bool
	identify := &cobra.Command{
		Use:   "identify [folder...]",
		Short: "Look up releases (Discogs, then MusicBrainz) and tag files (preview unless --apply)",
		Long: "Groups files by album and looks each album up. Confident matches are tagged\n" +
			"with the active preset (whole release: all fields from one source), covers are\n" +
			"downloaded (full size to cover.jpg). Weak matches go to `ymlib review`.\n" +
			"Files that already have a release ID are skipped unless --force.\n" +
			"Optional folders (relative to the library) limit the run.",
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			preset, _ := a.cfg.Preset(a.cfg.ActivePreset)
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			ctx, w, ew := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := scanQuietly(ctx, w, lib); err != nil {
				return err
			}
			ms, err := a.openMetadata(ctx, db, ew)
			if err != nil {
				return err
			}
			defer ms.Close()

			plan, err := lib.PlanIdentify(ctx, ms.identifier, library.IdentifyOptions{
				Prefixes: args, Force: force,
				Progress: func(done, total int, folder string) {
					fmt.Fprintf(ew, "[%d/%d] %s\n", done+1, total, folder)
				},
			})
			if err != nil {
				return err
			}
			files := 0
			for _, m := range plan.Matches {
				printAlbumMatch(w, m)
				files += len(m.Items)
			}
			for _, d := range plan.Reviews {
				fmt.Fprintf(w, "? %s: no confident match", d.Folder)
				if len(d.Candidates) > 0 {
					c := d.Candidates[0]
					fmt.Fprintf(w, " (best %.2f: %s — %s, %s)", c.Score, c.Release.Artist, c.Release.Title, c.Release.Provider)
				}
				fmt.Fprintln(w)
			}
			for _, p := range plan.Untitled {
				fmt.Fprintf(w, "- %s: no title tag, cannot be looked up\n", p)
			}
			for _, f := range plan.Failed {
				fmt.Fprintf(w, "! %s: %v\n", f.Path, f.Err)
			}
			fmt.Fprintf(w, "%d albums matched (%d files), %d need review, %d already identified, %d failed\n",
				len(plan.Matches), files, len(plan.Reviews), plan.Skipped, len(plan.Failed))
			if len(plan.Matches)+len(plan.Reviews) == 0 {
				return nil
			}
			if !apply {
				fmt.Fprintln(w, "Preview only. Run again with --apply to write tags and covers.")
				return nil
			}
			r, err := lib.ApplyIdentify(ctx, plan, preset, ms.images)
			if err != nil {
				return err
			}
			for _, warning := range r.Warnings {
				fmt.Fprintln(w, "warning:", warning)
			}
			fmt.Fprintf(w, "tagged %d files, %d albums added to review\n", r.Changed, r.Reviews)
			for _, f := range r.Failed {
				fmt.Fprintf(w, "! %s: %v\n", f.Path, f.Err)
			}
			fmt.Fprintln(w, "Run `ymlib organize` to rename files from their new tags.")
			if r.Reviews > 0 {
				fmt.Fprintln(w, "Run `ymlib review` to choose releases for the rest.")
			}
			if len(r.Failed) > 0 {
				return fmt.Errorf("%d files failed", len(r.Failed))
			}
			return nil
		},
	}
	identify.Flags().BoolVar(&force, "force", false, "also re-identify files that already have a release ID")
	identify.Flags().BoolVar(&apply, "apply", false, "write tags and covers")

	return []*cobra.Command{identify, newReviewCommand(a, loadConfig), newAuthCommand()}
}

func printAlbumMatch(w io.Writer, m library.AlbumMatch) {
	rel := m.Release
	fmt.Fprintf(w, "✓ %s  →  %s — %s", m.Folder, rel.Artist, rel.Title)
	if rel.Date != "" {
		fmt.Fprintf(w, " (%s)", rel.Date)
	}
	fmt.Fprintf(w, "  score %.2f, %s %s\n", m.Score, rel.Provider, rel.URL)
	for _, it := range m.Items {
		fmt.Fprintf(w, "  %s\n", it.Path)
		printChanges(w, it.Changes)
	}
	if rel.FrontURL != "" {
		fmt.Fprintf(w, "      + cover from %s\n", rel.FrontURL)
	}
	for _, p := range m.Unmatched {
		fmt.Fprintf(w, "  ? %s: no matching track on this release (left unchanged)\n", p)
	}
}

func newReviewCommand(a *app, loadConfig preRun) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "review",
		Short:   "List albums that need a manual release choice",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			reviews, err := lib.Reviews(cmd.Context())
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if len(reviews) == 0 {
				fmt.Fprintln(w, "Nothing to review.")
				return nil
			}
			for _, r := range reviews {
				fmt.Fprintf(w, "#%d  %s  (%d files; looked up: %s — %s)\n", r.ID, r.Folder, len(r.Paths), r.Query.Artist, orDash(r.Query.Album))
				if len(r.Candidates) == 0 {
					fmt.Fprintln(w, "    no candidates found")
				}
				for i, c := range r.Candidates {
					rel := c.Release
					details := []string{fmt.Sprintf("%d tracks", len(rel.Tracks))}
					for _, d := range []string{rel.Date, rel.ReleaseType, rel.Format, rel.Country} {
						if d != "" {
							details = append(details, d)
						}
					}
					fmt.Fprintf(w, "    %d. %.2f  %s — %s  [%s]  %s\n", i+1, c.Score, rel.Artist, rel.Title,
						strings.Join(details, ", "), rel.URL)
				}
			}
			fmt.Fprintln(w, "\nChoose:  ymlib review apply <#id> <candidate> [--apply]")
			fmt.Fprintln(w, "Dismiss: ymlib review skip <#id>")
			return nil
		},
	}

	var apply bool
	applyCmd := &cobra.Command{
		Use:     "apply <id> <candidate>",
		Short:   "Tag a review's files with the chosen candidate (preview unless --apply)",
		Args:    cobra.ExactArgs(2),
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			id, err1 := strconv.ParseInt(strings.TrimPrefix(args[0], "#"), 10, 64)
			n, err2 := strconv.Atoi(args[1])
			if err := errors.Join(err1, err2); err != nil {
				return fmt.Errorf("usage: ymlib review apply <id> <candidate>: %w", err)
			}
			preset, _ := a.cfg.Preset(a.cfg.ActivePreset)
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			ctx, w := cmd.Context(), cmd.OutOrStdout()
			ms, err := a.openMetadata(ctx, db, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			defer ms.Close()
			am, err := lib.PlanResolve(ctx, id, n, ms.identifier)
			if err != nil {
				return err
			}
			printAlbumMatch(w, am)
			if !apply {
				fmt.Fprintln(w, "Preview only. Run again with --apply to write tags and covers.")
				return nil
			}
			r, err := lib.ApplyResolve(ctx, id, am, preset, ms.images)
			if err != nil {
				return err
			}
			for _, warning := range r.Warnings {
				fmt.Fprintln(w, "warning:", warning)
			}
			fmt.Fprintf(w, "tagged %d files\n", r.Changed)
			for _, f := range r.Failed {
				fmt.Fprintf(w, "! %s: %v\n", f.Path, f.Err)
			}
			if len(r.Failed) > 0 {
				return fmt.Errorf("%d files failed; review #%d stays open", len(r.Failed), id)
			}
			return nil
		},
	}
	applyCmd.Flags().BoolVar(&apply, "apply", false, "write tags and covers")

	skip := &cobra.Command{
		Use:     "skip <id>",
		Short:   "Close a review without changing files",
		Args:    cobra.ExactArgs(1),
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			id, err := strconv.ParseInt(strings.TrimPrefix(args[0], "#"), 10, 64)
			if err != nil {
				return err
			}
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			if err := lib.SkipReview(cmd.Context(), id); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "review #%d skipped\n", id)
			return nil
		},
	}
	cmd.AddCommand(applyCmd, skip)
	return cmd
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func newAuthCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "Manage API credentials (stored in the OS keychain)"}

	var remove bool
	discogs := &cobra.Command{
		Use:   "discogs",
		Short: "Store your Discogs personal access token",
		Long: "Create a token at https://www.discogs.com/settings/developers, then run this\n" +
			"command and paste it. YMLIB_DISCOGS_TOKEN overrides the stored token.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			if remove {
				if err := secrets.Delete(secrets.DiscogsToken); err != nil {
					return err
				}
				fmt.Fprintln(w, "Discogs token removed from the keychain.")
				return nil
			}
			token, err := readSecret(cmd.InOrStdin(), w, "Discogs token: ")
			if err != nil {
				return err
			}
			if err := secrets.Set(secrets.DiscogsToken, token); err != nil {
				return err
			}
			fmt.Fprintln(w, "Discogs token saved to the keychain.")
			return nil
		},
	}
	discogs.Flags().BoolVar(&remove, "remove", false, "delete the stored token")

	status := &cobra.Command{
		Use:   "status",
		Short: "Show which credentials are available",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, src, err := secrets.Get(secrets.DiscogsToken)
			if err != nil {
				return err
			}
			if src == secrets.SourceNone {
				fmt.Fprintln(cmd.OutOrStdout(), "discogs:     no token (run `ymlib auth discogs`)")
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "discogs:     token from %s\n", src)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "musicbrainz: no credentials needed")
			return nil
		},
	}
	cmd.AddCommand(discogs, status)
	return cmd
}

// readSecret reads one line, without echo when in is a terminal.
func readSecret(in io.Reader, w io.Writer, prompt string) (string, error) {
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Fprint(w, prompt)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(w)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if s := strings.TrimSpace(line); s != "" {
		return s, nil
	}
	return "", errors.New("no token given")
}
