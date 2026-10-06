package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/BurntSushi/toml"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/spf13/cobra"
)

func (a *App) newConfigCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "config",
		Short: "print the effective config and resolved paths",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := a.Env()
			if err != nil {
				return err
			}
			if a.Flags.JSON {
				return a.printConfigJSON(env)
			}
			enc := toml.NewEncoder(a.Out)
			if err := enc.Encode(env.Cfg); err != nil {
				return err
			}
			fmt.Fprintln(a.Out)
			fmt.Fprintln(a.Out, "# resolved paths")
			tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "config\t%s\n", env.Paths.ConfigFile)
			fmt.Fprintf(tw, "whitelist\t%s\n", env.Paths.Whitelist)
			fmt.Fprintf(tw, "journal\t%s\n", env.Paths.Journal)
			fmt.Fprintf(tw, "cache\t%s\n", env.Paths.CacheDir)
			fmt.Fprintf(tw, "state\t%s\n", env.Paths.StateDir)
			fmt.Fprintf(tw, "data\t%s\n", env.Paths.DataDir)
			fmt.Fprintf(tw, "trash\t%s\n", env.Paths.TrashDir)
			fmt.Fprintf(tw, "distro\t%s\n", env.Distro.String())
			fmt.Fprintf(tw, "package-manager\t%s\n", env.Adapter.ID())
			return tw.Flush()
		},
	}
}

func (a *App) printConfigJSON(env *engine.Env) error {
	out := map[string]any{
		"config":  env.Cfg,
		"paths":   env.Paths,
		"distro":  env.Distro,
		"adapter": env.Adapter.ID(),
	}
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
