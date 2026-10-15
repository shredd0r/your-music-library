// Package cli defines the ymlib command tree.
package cli

import (
	"github.com/spf13/cobra"

	"your-music-library/internal/config"
	"your-music-library/internal/platform"
)

// app carries state shared by commands, resolved before each command runs.
type app struct {
	configPath string
	dirs       platform.Dirs
	cfg        config.Config
	endpoints  endpoints
}

// version is reported by --version and sent in the User-Agent.
const version = "0.1.0-dev"

// NewRootCommand builds the command tree. dirs is injected so tests can use
// temporary directories.
func NewRootCommand(dirs platform.Dirs) *cobra.Command {
	return newRoot(&app{dirs: dirs})
}

func newRoot(a *app) *cobra.Command {
	dirs := a.dirs
	root := &cobra.Command{
		Use:           "ymlib",
		Version:       version,
		Short:         "Your music library: download, tag, organize and sync music",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&a.configPath, "config", dirs.ConfigFile(), "path to config.yaml")

	// loadConfig is attached to commands that need a valid config; `config
	// init` and `config path` deliberately work without one.
	loadConfig := func(*cobra.Command, []string) error {
		c, err := config.Load(a.configPath)
		if err != nil {
			return err
		}
		a.cfg = c
		return nil
	}

	root.AddCommand(newConfigCommand(a, loadConfig), newPresetCommand(a, loadConfig))
	root.AddCommand(newLibraryCommands(a, loadConfig)...)
	root.AddCommand(newMetadataCommands(a, loadConfig)...)
	root.AddCommand(newDownloadCommands(a, loadConfig)...)
	return root
}
