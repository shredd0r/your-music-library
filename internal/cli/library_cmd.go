package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"errors"
	"your-music-library/internal/cleanup"
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
		return nil, nil, errors.Join(err, db.Close())
	}
	return lib, db, nil
}

func newLibraryCommands(a *app, loadConfig preRun) []*cobra.Command {
	scan := &cobra.Command{
		Use:     "scan",
		Short:   "Index the library folder (reads tags, never changes files)",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
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
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
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

	var retagApply bool
	retag := &cobra.Command{
		Use:   "retag",
		Short: "Bring all files to the fixed tag format (preview unless --apply)",
		Long: "Removes track/disc totals and embedded back covers, pads numbers (01),\n" +
			"converts typographic quotes and dashes to ASCII, moves guests from Artist into\n" +
			"the title (feat.), restores grouped singles and shrinks embedded covers to\n" +
			"600 px. Before an embedded cover is shrunk, the full-size image is saved as\n" +
			"cover.jpg in the album folder if none exists. Other tags are kept.",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) (err error) {
			lib, db, err := a.openLibrary()
			if err != nil {
				return err
			}
			defer cleanup.Close(&err, db)
			ctx, w := cmd.Context(), cmd.OutOrStdout()
			if err := scanQuietly(ctx, w, lib); err != nil {
				return err
			}
			p, err := lib.PlanRetag(ctx)
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
			fmt.Fprintf(w, "%d to change, %d already match, %d unreadable\n", len(p.Items), p.Unchanged, len(p.Failed))
			if len(p.Items) == 0 {
				return nil
			}
			if !retagApply {
				fmt.Fprintln(w, "Preview only. Run again with --apply to write the tags.")
				return nil
			}
			r, err := lib.ApplyRetag(ctx, p)
			if err != nil {
				return err
			}
			return reportApply(w, "retagged", r)
		},
	}
	retag.Flags().BoolVar(&retagApply, "apply", false, "write the tags")

	return []*cobra.Command{scan, organize, retag}
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
