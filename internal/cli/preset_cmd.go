package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newPresetCommand(a *app, loadConfig preRun) *cobra.Command {
	cmd := &cobra.Command{Use: "preset", Short: "Inspect metadata presets"}

	list := &cobra.Command{
		Use:     "list",
		Short:   "List presets; the active one is marked with *",
		Args:    cobra.NoArgs,
		PreRunE: loadConfig,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "\tNAME\tKIND\tCOVER\tFIELDS")
			for _, name := range a.cfg.PresetNames() {
				p, _ := a.cfg.Preset(name)
				active, kind, cover := "", "user", "original"
				if name == a.cfg.ActivePreset {
					active = "*"
				}
				if p.Builtin {
					kind = "built-in"
				}
				if p.CoverMaxPx > 0 {
					cover = fmt.Sprintf("%dpx", p.CoverMaxPx)
				}
				fields := make([]string, len(p.Fields))
				for i, f := range p.Fields {
					fields[i] = string(f)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", active, name, kind, cover, strings.Join(fields, ", "))
			}
			return tw.Flush()
		},
	}

	cmd.AddCommand(list)
	return cmd
}
