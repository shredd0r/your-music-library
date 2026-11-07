package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"your-music-library/internal/config"
	"your-music-library/internal/source"
)

func newPluginCommand(a *app, loadConfig preRun) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage source plugins (HTTP servers you run)",
		Long: `A plugin is an HTTP server you start yourself; ymlib only keeps its
address and talks to it (protocol: docs/plugin-protocol.md). Plugins are
registered in config.yaml under plugins.registered.`,
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE:    func(cmd *cobra.Command, _ []string) error { return listPlugins(cmd, a) },
	}

	list := &cobra.Command{
		Use:     "list",
		Short:   "List plugins with their status",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE:    func(cmd *cobra.Command, _ []string) error { return listPlugins(cmd, a) },
	}

	add := &cobra.Command{
		Use:   "add <name> <url>",
		Short: "Register a plugin server, e.g. `ymlib plugin add shelf http://127.0.0.1:8765`",
		Long: `Registers a plugin under name (used in references, name:id) and enables
it. The server doesn't have to be running; its health is checked and shown.`,
		Args:    cobra.ExactArgs(2),
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, url := args[0], args[1]
			c, err := config.AddPlugin(a.configPath, name, url)
			if err != nil {
				return err
			}
			a.cfg = c
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "Registered %s at %s.\n", name, url)
			p := &source.Plugin{Name: name, URL: url, Enabled: true}
			h, err := p.Health(cmd.Context())
			switch {
			case err == nil:
				fmt.Fprintf(w, "It is online: %s %s.\n", orDash(h.Name), orDash(h.Version))
			case source.IsOffline(err):
				fmt.Fprintf(w, "It is offline now; start it before searching or downloading.\n")
			default:
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
			}
			return nil
		},
	}

	remove := &cobra.Command{
		Use:   "remove <name>",
		Short: "Unregister a plugin; its queued downloads fail on the next queue run",
		Args:  cobra.ExactArgs(1),
		// works with a config whose plugin entry is broken
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := config.RemovePlugin(a.configPath, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s.\n", args[0])
			return nil
		},
	}

	setEnabled := func(use, short string, enabled bool) *cobra.Command {
		return &cobra.Command{
			Use:     use + " <name>",
			Short:   short,
			Args:    cobra.ExactArgs(1),
			PreRunE: loadConfig,
			RunE: func(cmd *cobra.Command, args []string) error {
				if _, err := config.SetPluginEnabled(a.configPath, args[0], enabled); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s is %s.\n", args[0], map[bool]string{true: "enabled", false: "disabled"}[enabled])
				return nil
			},
		}
	}

	cmd.AddCommand(list, add, remove,
		setEnabled("enable", "Enable a plugin", true),
		setEnabled("disable", "Disable a plugin; its queued downloads wait", false))
	return cmd
}

func listPlugins(cmd *cobra.Command, a *app) error {
	w := cmd.OutOrStdout()
	reg := a.registry()
	if len(reg.All()) == 0 {
		fmt.Fprintln(w, "No plugins registered. A plugin is an HTTP server you run; register it with")
		fmt.Fprintln(w, "`ymlib plugin add <name> <url>`. The protocol is in docs/plugin-protocol.md.")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tURL\tENABLED\tSTATUS")
	for _, st := range source.Check(cmd.Context(), reg.All()) {
		enabled := "yes"
		if !st.Plugin.Enabled {
			enabled = "no"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", st.Plugin.Name, st.Plugin.URL, enabled, statusText(st))
	}
	return tw.Flush()
}

// statusText describes a health check for listings.
func statusText(st source.Status) string {
	switch {
	case st.Online():
		return fmt.Sprintf("online (%s %s)", orDash(st.Health.Name), orDash(st.Health.Version))
	case source.IsOffline(st.Err):
		return "offline"
	}
	return "error: " + st.Err.Error()
}
