package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"your-music-library/internal/config"
	"your-music-library/internal/library"
	"your-music-library/internal/platform"
	"your-music-library/internal/store"
	"your-music-library/internal/tags"
)

// openLibrary opens the index and the library folder from the config.
// The caller must close the returned DB.
func (a *app) openLibrary() (*library.Library, *sql.DB, error) {
	root, err := platform.ExpandHome(a.cfg.LibraryPath)
	if err != nil {
		return nil, nil, err
	}
	db, err := store.Open(a.dirs.DatabaseFile())
	if err != nil {
		return nil, nil, err
	}
	lib, err := library.Open(root, db, a.cfg.PathTemplate)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return lib, db, nil
}

func newLibraryCommands(a *app, loadConfig preRun) []*cobra.Command {
	scan := &cobra.Command{
		Use:     "scan",
		Short:   "Index the library folder (reads tags, never changes files)",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer db.Close()
			r, err := lib.Scan(cmd.Context())
			if err != nil {
				return err
			}
			printScan(cmd.OutOrStdout(), r, true)
			return nil
		},
	}

	var organizeApply bool
	organize := &cobra.Command{
		Use:   "organize",
		Short: "Move files to the paths given by path_template (preview unless --apply)",
		Long: "Scans the library, then shows where each file would move according to\n" +
			"path_template. Nothing moves without --apply. Existing files are never overwritten.",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer db.Close()
			ctx, w := cmd.Context(), cmd.OutOrStdout()
			if err := scanQuietly(ctx, w, lib); err != nil {
				return err
			}
			p, err := lib.PlanOrganize(ctx)
			if err != nil {
				return err
			}
			for _, m := range p.Moves {
				fmt.Fprintf(w, "  %s\n    → %s\n", m.From, m.To)
			}
			for _, m := range p.Images {
				fmt.Fprintf(w, "  %s\n    → %s\n", m.From, m.To)
			}
			for _, c := range p.Conflicts {
				fmt.Fprintf(w, "! %s\n    → %s (skipped: %s)\n", c.From, c.To, c.Reason)
			}
			fmt.Fprintf(w, "%d to move, %d folder images, %d conflicts, %d already in place\n",
				len(p.Moves), len(p.Images), len(p.Conflicts), p.Unchanged)
			if len(p.Moves)+len(p.Images) == 0 {
				return nil
			}
			if !organizeApply {
				fmt.Fprintln(w, "Preview only. Run again with --apply to move the files.")
				return nil
			}
			r, err := lib.ApplyOrganize(ctx, p)
			if err != nil {
				return err
			}
			return reportApply(w, "moved", r)
		},
	}
	organize.Flags().BoolVar(&organizeApply, "apply", false, "move the files")

	var retagPreset string
	var retagApply bool
	retag := &cobra.Command{
		Use:   "retag",
		Short: "Rewrite tags of all files with a preset (preview unless --apply)",
		Long: "Removes fields the preset does not include and shrinks embedded covers to\n" +
			"the preset's size. Before an embedded cover is shrunk or removed, the full-size\n" +
			"image is saved as cover.jpg/back.jpg in the album folder if none exists.",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name := retagPreset
			if name == "" {
				name = a.cfg.ActivePreset
			}
			preset, ok := a.cfg.Preset(name)
			if !ok {
				return fmt.Errorf("unknown preset %q (see: ymlib preset list)", name)
			}
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer db.Close()
			ctx, w := cmd.Context(), cmd.OutOrStdout()
			if err := scanQuietly(ctx, w, lib); err != nil {
				return err
			}
			p, err := lib.PlanRetag(ctx, preset)
			if err != nil {
				return err
			}
			for _, it := range p.Items {
				fmt.Fprintf(w, "  %s\n", it.Path)
				printChanges(w, it.Changes)
				for _, img := range it.SaveImages {
					fmt.Fprintf(w, "      + save full-size %s\n", img)
				}
			}
			for _, f := range p.Failed {
				fmt.Fprintf(w, "! %s: %v\n", f.Path, f.Err)
			}
			fmt.Fprintf(w, "preset %q: %d to change, %d already match, %d unreadable\n",
				name, len(p.Items), p.Unchanged, len(p.Failed))
			if len(p.Items) == 0 {
				return nil
			}
			if !retagApply {
				fmt.Fprintln(w, "Preview only. Run again with --apply to write the tags.")
				return nil
			}
			r, err := lib.ApplyRetag(ctx, p, preset)
			if err != nil {
				return err
			}
			return reportApply(w, "retagged", r)
		},
	}
	retag.Flags().StringVar(&retagPreset, "preset", "", "preset to apply (default: active_preset)")
	retag.Flags().BoolVar(&retagApply, "apply", false, "write the tags")

	var singlesApply bool
	singles := &cobra.Command{
		Use:   "singles [as-album|grouped]",
		Short: "Convert singles between as-album and grouped (preview unless --apply)",
		Long: "grouped: each artist's singles become one album \"<Artist> Singles\", numbered\n" +
			"by release date. as-album: every single gets its own album back.\n" +
			"Only tags change; run `ymlib organize` afterwards to move the files.\n" +
			"Without an argument the mode from config (singles:) is used.",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{string(config.SinglesAsAlbum), string(config.SinglesGrouped)},
		PreRunE:   loadConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := a.cfg.Singles
			if len(args) == 1 {
				mode = config.SinglesMode(args[0])
				if mode != config.SinglesAsAlbum && mode != config.SinglesGrouped {
					return fmt.Errorf("mode must be %q or %q", config.SinglesAsAlbum, config.SinglesGrouped)
				}
			}
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer db.Close()
			ctx, w := cmd.Context(), cmd.OutOrStdout()
			if err := scanQuietly(ctx, w, lib); err != nil {
				return err
			}
			items, err := lib.PlanSingles(ctx, mode)
			if err != nil {
				return err
			}
			for _, it := range items {
				fmt.Fprintf(w, "  %s\n", it.Path)
				printChanges(w, it.Changes)
			}
			fmt.Fprintf(w, "singles %s: %d files to change\n", mode, len(items))
			if len(items) == 0 {
				return nil
			}
			if !singlesApply {
				fmt.Fprintln(w, "Preview only. Run again with --apply to write the tags.")
				return nil
			}
			r, err := lib.ApplySingles(ctx, items)
			if err != nil {
				return err
			}
			if err := reportApply(w, "retagged", r); err != nil {
				return err
			}
			fmt.Fprintln(w, "Run `ymlib organize` to move the files into their new folders.")
			return nil
		},
	}
	singles.Flags().BoolVar(&singlesApply, "apply", false, "write the tags")

	return []*cobra.Command{scan, organize, retag, singles}
}

// scanQuietly refreshes the index before planning and prints one line.
func scanQuietly(ctx context.Context, w io.Writer, lib *library.Library) error {
	r, err := lib.Scan(ctx)
	if err != nil {
		return err
	}
	printScan(w, r, false)
	return nil
}

func printScan(w io.Writer, r library.ScanReport, verbose bool) {
	fmt.Fprintf(w, "scan: %d added, %d updated, %d unchanged, %d removed",
		r.Added, r.Updated, r.Unchanged, r.Removed)
	if n := len(r.Unsupported); n > 0 {
		fmt.Fprintf(w, ", %d unsupported", n)
	}
	if n := len(r.Failed); n > 0 {
		fmt.Fprintf(w, ", %d unreadable", n)
	}
	fmt.Fprintln(w)
	if !verbose {
		return
	}
	for _, p := range r.Unsupported {
		fmt.Fprintf(w, "  unsupported format: %s\n", p)
	}
	for _, f := range r.Failed {
		fmt.Fprintf(w, "  unreadable: %s: %v\n", f.Path, f.Err)
	}
}

func printChanges(w io.Writer, changes []tags.Change) {
	for _, c := range changes {
		old, new := c.Old, c.New
		if old == "" {
			old = "(none)"
		}
		if new == "" {
			new = "(removed)"
		}
		fmt.Fprintf(w, "      %-13s %s → %s\n", c.Field+":", old, new)
	}
}

func reportApply(w io.Writer, verb string, r library.ApplyReport) error {
	fmt.Fprintf(w, "%s %d\n", verb, r.Changed)
	for _, f := range r.Failed {
		fmt.Fprintf(w, "! %s: %v\n", f.Path, f.Err)
	}
	if len(r.Failed) > 0 {
		return fmt.Errorf("%d files failed", len(r.Failed))
	}
	return nil
}
