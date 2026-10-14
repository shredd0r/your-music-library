package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"your-music-library/internal/config"
)

type preRun = func(*cobra.Command, []string) error

func newConfigCommand(a *app, loadConfig preRun) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Show or create the configuration"}

	show := &cobra.Command{
		Use:     "show",
		Short:   "Print the effective configuration (defaults merged with config.yaml)",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out, err := a.cfg.Marshal()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(out)
			return err
		},
	}

	path := &cobra.Command{
		Use:   "path",
		Short: "Print where ymlib keeps its config, database and cache",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "config:   %s\n", a.configPath)
			fmt.Fprintf(w, "database: %s\n", a.dirs.DatabaseFile())
			fmt.Fprintf(w, "cache:    %s\n", a.dirs.Cache)
			return nil
		},
	}

	var force bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Write a default config.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := os.Stat(a.configPath); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", a.configPath)
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			out, err := config.Default().Marshal()
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(a.configPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(a.configPath, out, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", a.configPath)
			return nil
		},
	}
	initCmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config.yaml")

	cmd.AddCommand(show, path, initCmd)
	return cmd
}
