package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"your-music-library/internal/cleanup"
	"your-music-library/internal/config"
	"your-music-library/internal/download"
	"your-music-library/internal/library"
	"your-music-library/internal/queue"
	"your-music-library/internal/source"
)

func (a *app) sourcesDir() string {
	if a.cfg.SourcesDir != "" {
		if d, err := expandHome(a.cfg.SourcesDir); err == nil {
			return d
		}
	}
	return filepath.Join(filepath.Dir(a.configPath), "sources")
}

// registry finds the source plugins. Callers Close it when done, which
// stops plugins that keep running (protocol 2).
func (a *app) registry() (*source.Registry, error) {
	plugins, err := source.Discover(a.sourcesDir())
	if err != nil {
		return nil, err
	}
	reg := source.NewRegistry(plugins)
	reg.SetTimeouts(time.Duration(a.cfg.Sources.IdleMinutes)*time.Minute, time.Duration(a.cfg.Sources.CloseSeconds)*time.Second)
	return reg, nil
}

// withInterrupt cancels ctx on Ctrl-C so jobs stop cleanly and resume later.
func withInterrupt(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt)
}

func newDownloadCommands(a *app, loadConfig preRun) []*cobra.Command {
	sources := &cobra.Command{
		Use:     "sources",
		Short:   "List source plugins",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			reg, err := a.registry()
			if err != nil {
				return err
			}
			defer reg.Close()
			if len(reg.All()) == 0 {
				fmt.Fprintf(w, "No source plugins in %s.\n", a.sourcesDir())
				fmt.Fprintln(w, "A plugin is an executable speaking the JSON protocol described in internal/source/source.go.")
				return nil
			}
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tVERSION\tPROTOCOL\tSTATUS")
			for _, p := range reg.All() {
				info, err := p.Info(cmd.Context())
				status := "ok"
				if err != nil {
					status = "error: " + err.Error()
				}
				protocol := "—"
				switch info.Protocol {
				case 1:
					protocol = "1 (per request)"
				case 2:
					protocol = "2 (stays running)"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Name, orDash(info.Version), protocol, status)
			}
			return tw.Flush()
		},
	}

	var searchAlbum bool
	var searchSource string
	var searchLimit int
	search := &cobra.Command{
		Use:     "search [--album] [--source NAME] <query...>",
		Short:   "Search source plugins; ✓ = in library, ≈ = other edition in library",
		Args:    cobra.MinimumNArgs(1),
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			reg, err := a.registry()
			if err != nil {
				return err
			}
			defer reg.Close()
			plugins := reg.All()
			if searchSource != "" {
				p, err := reg.Get(searchSource)
				if err != nil {
					return err
				}
				plugins = []*source.Plugin{p}
			}
			if len(plugins) == 0 {
				return fmt.Errorf("no source plugins in %s (see `ymlib sources`)", a.sourcesDir())
			}
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			ctx, w := cmd.Context(), cmd.OutOrStdout()
			dups, err := lib.DupIndex(ctx)
			if err != nil {
				return err
			}
			kind := source.KindTrack
			if searchAlbum {
				kind = source.KindAlbum
			}
			query := strings.Join(args, " ")
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "\tREF\tARTIST\tTITLE\tLENGTH\tQUALITY\tE")
			total := 0
			for _, p := range plugins {
				items, err := p.Search(ctx, query, kind, searchLimit)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
					continue
				}
				for _, it := range items {
					flag := " "
					if kind == source.KindTrack {
						st, _ := dups.Check(library.DupQuery{Source: p.Name, SourceTrackID: it.ID, ISRC: it.ISRC,
							Artist: it.Artist, AlbumArtist: it.AlbumArtist, Album: it.Album, Title: it.Title})
						flag = st.Symbol()
					}
					title := it.Title
					if kind == source.KindTrack && it.Album != "" {
						title += "  [" + it.Album + "]"
					}
					if kind == source.KindAlbum {
						if it.Year > 0 {
							title += fmt.Sprintf(" (%d)", it.Year)
						}
						if it.TrackCount > 0 {
							title += fmt.Sprintf(", %d tracks", it.TrackCount)
						}
					}
					explicit := ""
					if it.Explicit {
						explicit = "E"
					}
					fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", flag, source.Ref{Source: p.Name, ID: it.ID},
						it.Artist, title, clock(it.Duration()), it.Quality, explicit)
					total++
				}
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if total == 0 {
				fmt.Fprintln(w, "No results.")
				return nil
			}
			flag := ""
			if searchAlbum {
				flag = "--album "
			}
			fmt.Fprintf(w, "\nDownload: ymlib download %s<REF>...\n", flag)
			return nil
		},
	}
	search.Flags().BoolVar(&searchAlbum, "album", false, "search albums instead of tracks")
	search.Flags().StringVar(&searchSource, "source", "", "search only this source plugin")
	search.Flags().IntVar(&searchLimit, "limit", 20, "results per source")

	var dlAlbum, dlForce, dlNoRun bool
	dl := &cobra.Command{
		Use:   "download [--album] <source:id>...",
		Short: "Queue tracks or albums and download them into the library",
		Long: "Adds the references to the download queue and runs it. Each track is\n" +
			"downloaded, verified, identified (Discogs/MusicBrainz), tagged with the active\n" +
			"preset and filed by path_template. Tracks already in the library are skipped\n" +
			"unless --force. Interrupted downloads resume with `ymlib queue run`.",
		Args:    cobra.MinimumNArgs(1),
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			kind := source.KindTrack
			if dlAlbum {
				kind = source.KindAlbum
			}
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			q := queue.New(db)
			w := cmd.OutOrStdout()
			for _, arg := range args {
				ref, err := source.ParseRef(arg)
				if err != nil {
					return err
				}
				j, created, err := q.Enqueue(cmd.Context(), ref.Source, kind, source.Item{ID: ref.ID}, 0)
				if err != nil {
					return err
				}
				switch {
				case created:
					fmt.Fprintf(w, "queued %s %s (job %d)\n", kind, ref, j.ID)
				case j.Status == queue.Done:
					fmt.Fprintf(w, "%s was already downloaded (job %d: %s)\n", ref, j.ID, j.Note)
				default:
					fmt.Fprintf(w, "%s is already queued (job %d, %s)\n", ref, j.ID, j.Status)
				}
			}
			if dlNoRun {
				return nil
			}
			return a.runQueue(cmd, lib, db, dlForce)
		},
	}
	dl.Flags().BoolVar(&dlAlbum, "album", false, "references are albums")
	dl.Flags().BoolVar(&dlForce, "force", false, "download tracks even if they are already in the library")
	dl.Flags().BoolVar(&dlNoRun, "no-run", false, "only add to the queue")

	return []*cobra.Command{sources, search, dl, newQueueCommand(a, loadConfig), newImportCommand(a, loadConfig)}
}

func clock(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	s := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// runQueue processes the download queue with the configured providers.
func (a *app) runQueue(cmd *cobra.Command, lib *library.Library, db *sql.DB, force bool) error {
	reg, err := a.registry()
	if err != nil {
		return err
	}
	defer reg.Close()
	ctx, stop := withInterrupt(cmd.Context())
	defer stop()
	w, ew := cmd.OutOrStdout(), cmd.ErrOrStderr()
	preset, _ := a.cfg.Preset(a.cfg.ActivePreset)
	r := &download.Runner{
		Queue: queue.New(db), Sources: reg, Lib: lib, Preset: preset,
		Parallel: a.cfg.Queue.Parallel, Force: force,
		Log: func(format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) },
	}
	ms, err := a.openMetadata(ctx, db, ew)
	if err != nil {
		fmt.Fprintf(ew, "warning: %v; files keep the source's tags\n", err)
	} else {
		defer ms.Close()
		r.Identifier, r.Images = ms.identifier, ms.images
	}
	sum, err := r.Run(ctx)
	fmt.Fprintf(w, "imported %d, already in library %d, failed %d\n", sum.Imported, sum.Skipped, sum.Failed)
	if ctx.Err() != nil {
		fmt.Fprintln(w, "Interrupted. Run `ymlib queue run` to continue.")
		return nil
	}
	if err != nil {
		return err
	}
	if a.cfg.Singles == config.SinglesGrouped && sum.Imported > 0 {
		fmt.Fprintln(w, "Singles are grouped: run `ymlib singles` and `ymlib organize` to file new singles.")
	}
	if sum.Failed > 0 {
		fmt.Fprintln(w, "See `ymlib queue` for errors; `ymlib queue retry` tries failed jobs again.")
	}
	return nil
}

func newQueueCommand(a *app, loadConfig preRun) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "queue",
		Short:   "Show the download queue",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			_, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			jobs, err := queue.New(db).List(cmd.Context())
			if err != nil {
				return err
			}
			return printJobs(cmd.OutOrStdout(), jobs)
		},
	}
	var force bool
	run := &cobra.Command{
		Use:     "run",
		Short:   "Process pending jobs (resumes interrupted downloads)",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			return a.runQueue(cmd, lib, db, force)
		},
	}
	run.Flags().BoolVar(&force, "force", false, "download tracks even if they are already in the library")

	retry := &cobra.Command{
		Use:     "retry [job-id]",
		Short:   "Reset failed jobs (all, or one job/album) to pending",
		Args:    cobra.MaximumNArgs(1),
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			var id int64
			if len(args) == 1 {
				var err error
				if id, err = strconv.ParseInt(args[0], 10, 64); err != nil {
					return err
				}
			}
			_, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			n, err := queue.New(db).Retry(cmd.Context(), id)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d jobs reset; run `ymlib queue run`\n", n)
			return nil
		},
	}
	var clearFailed bool
	clear := &cobra.Command{
		Use:     "clear",
		Short:   "Remove finished jobs from the list",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			_, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			n, err := queue.New(db).Clear(cmd.Context(), clearFailed)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%d jobs removed\n", n)
			return nil
		},
	}
	clear.Flags().BoolVar(&clearFailed, "failed", false, "also remove failed jobs")
	cmd.AddCommand(run, retry, clear)
	return cmd
}

func printJobs(w io.Writer, jobs []queue.Job) error {
	if len(jobs) == 0 {
		fmt.Fprintln(w, "The queue is empty.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tREF\tITEM\tRESULT")
	for _, j := range jobs {
		name := strings.TrimSpace(j.Item.Artist + " — " + j.Item.Title)
		if j.Item.Title == "" {
			name = "(not resolved yet)"
		}
		ref := j.Ref().String()
		if j.ParentID != 0 {
			ref = "  " + ref
		}
		result := j.Note
		if j.File != "" && j.Note != "downloaded" {
			result = strings.TrimSpace(j.Note + ": " + j.File)
		}
		if j.LastError != "" {
			result = fmt.Sprintf("attempt %d: %s", j.Attempts, j.LastError)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", j.ID, j.Status, ref, name, result)
	}
	return tw.Flush()
}

func newImportCommand(a *app, loadConfig preRun) *cobra.Command {
	var apply, move, force, noLookup bool
	cmd := &cobra.Command{
		Use:   "import <folder>",
		Short: "Import audio files from a folder into the library (preview unless --apply)",
		Long: "Reads the files' tags, skips tracks already in the library, identifies albums\n" +
			"(Discogs/MusicBrainz), tags them with the active preset and copies them to\n" +
			"their path_template location. Sources are kept unless --move.",
		Args:    cobra.ExactArgs(1),
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			dir, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			files, err := library.CollectAudio(dir)
			if err != nil {
				return err
			}
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			if within(dir, lib.Root()) {
				return fmt.Errorf("%s is inside the library; use `ymlib identify` and `ymlib organize` instead", dir)
			}
			ctx, w, ew := cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := scanQuietly(ctx, w, lib); err != nil {
				return err
			}
			var ms *metadataSession
			if !noLookup {
				if ms, err = a.openMetadata(ctx, db, ew); err != nil {
					return err
				}
				defer ms.Close()
			}
			opts := library.IngestOptions{Base: dir, Force: force, Move: move,
				Progress: func(done, total int, folder string) { fmt.Fprintf(ew, "[%d/%d] %s\n", done+1, total, folder) }}
			var plan library.IngestPlan
			if ms != nil {
				plan, err = lib.PlanIngest(ctx, files, ms.identifier, opts)
			} else {
				plan, err = lib.PlanIngest(ctx, files, nil, opts)
			}
			if err != nil {
				return err
			}
			for _, m := range plan.Matches {
				fmt.Fprintf(w, "✓ %s  →  %s — %s (%s)  score %.2f, %s\n", m.Folder, m.Release.Artist, m.Release.Title,
					orDash(m.Release.Date), m.Score, m.Release.Provider)
			}
			for _, d := range plan.Reviews {
				fmt.Fprintf(w, "? %s: no confident match; imported with its own tags and listed in `ymlib review`\n", d.Folder)
			}
			for _, it := range plan.Items {
				flag := ""
				if it.Dup == library.OtherEdition {
					flag = "  (≈ other edition in library: " + it.DupPath + ")"
				}
				fmt.Fprintf(w, "  %s\n    → %s%s\n", it.Display, it.Target, flag)
			}
			for _, it := range plan.Duplicates {
				fmt.Fprintf(w, "= %s: already in library (%s)\n", it.Display, it.DupPath)
			}
			for _, c := range plan.Conflicts {
				fmt.Fprintf(w, "! %s → %s: %s\n", c.From, c.To, c.Reason)
			}
			for _, u := range plan.Unsupported {
				fmt.Fprintf(w, "- %s: unsupported format\n", u)
			}
			for _, f := range plan.Failed {
				fmt.Fprintf(w, "! %s: %v\n", f.Path, f.Err)
			}
			fmt.Fprintf(w, "%d to import, %d already in library, %d conflicts, %d unsupported, %d unreadable\n",
				len(plan.Items), len(plan.Duplicates), len(plan.Conflicts), len(plan.Unsupported), len(plan.Failed))
			if len(plan.Items) == 0 {
				return nil
			}
			if !apply {
				fmt.Fprintln(w, "Preview only. Run again with --apply to import.")
				return nil
			}
			preset, _ := a.cfg.Preset(a.cfg.ActivePreset)
			var images library.ImageSource
			if ms != nil {
				images = ms.images
			}
			rep, err := lib.ApplyIngest(ctx, plan, preset, images, opts)
			if err != nil {
				return err
			}
			for _, warn := range rep.Warnings {
				fmt.Fprintln(w, "warning:", warn)
			}
			for _, f := range rep.Failed {
				fmt.Fprintf(w, "! %s: %v\n", f.Path, f.Err)
			}
			fmt.Fprintf(w, "imported %d files, %d albums added to review\n", len(rep.Imported), rep.Reviews)
			if len(rep.Failed) > 0 {
				return fmt.Errorf("%d files failed", len(rep.Failed))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&apply, "apply", false, "import the files")
	cmd.Flags().BoolVar(&move, "move", false, "delete source files after importing them")
	cmd.Flags().BoolVar(&force, "force", false, "import files that are already in the library")
	cmd.Flags().BoolVar(&noLookup, "no-lookup", false, "keep the files' own tags (no Discogs/MusicBrainz)")
	return cmd
}

func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
