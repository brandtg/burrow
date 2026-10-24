package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/brandtg/burrow/internal/catalog/projects"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/spf13/cobra"
)

// purgeOptions are the flags the deprecated purge alias still accepts.
type purgeOptions struct {
	paths bool
	age   string
}

// newPurgeCmd builds the deprecated `burrow purge` alias. It behaves exactly
// like `burrow clean --only projects` (one notice on stderr first). Artifacts
// move to the Trash and are recoverable; --age and --paths keep working.
func (a *App) newPurgeCmd() *cobra.Command {
	var opts purgeOptions
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "deprecated alias for: burrow clean --only projects",
		Long: `Deprecated alias for ` + "`burrow clean --only projects`" + `.

Finds rebuildable build artifacts (node_modules, target, virtualenvs,
__pycache__, ...) under the configured project roots and moves them to the
Trash. Artifacts are kept when they are git-tracked, when git state cannot be
determined, when they contain a git repository, when their activity age cannot
be verified, or when they have seen file activity within the age window.
Trashed artifacts are recoverable (burrow history --restore); nothing is
permanently deleted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := a.Env()
			if err != nil {
				return err
			}
			if opts.paths {
				return a.printPurgePaths(env, projects.Roots(env))
			}
			if opts.age != "" {
				if _, err := config.ParseDuration(opts.age); err != nil {
					return &ExitError{Code: ExitUsage, Err: err}
				}
				// An explicit --age overrides the fallback window.
				env.Cfg.Purge.MaxAge = opts.age
			}
			fmt.Fprintln(a.ErrOut, "notice: 'burrow purge' is deprecated; running the equivalent of 'burrow clean --only projects'")
			a.Flags.Only = []string{projects.ID}
			return a.runClean(cleanFlags{})
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.paths, "paths", false, "print the configured project roots")
	f.StringVar(&opts.age, "age", "", "only artifacts with no activity in this window (default 7d)")
	return cmd
}

func (a *App) printPurgePaths(env *engine.Env, roots []string) error {
	if a.Flags.JSON {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"paths": roots, "config": env.Paths.ConfigFile})
	}
	fmt.Fprintf(a.Out, "project roots (from %s):\n", env.Paths.ConfigFile)
	for _, r := range roots {
		state := "missing"
		if fi, err := os.Stat(r); err == nil && fi.IsDir() {
			state = "ok"
		}
		fmt.Fprintf(a.Out, "  %s  [%s]\n", r, state)
	}
	fmt.Fprintf(a.Out, "\nedit %s to change them, or set BURROW_PURGE_PATHS\n", env.Paths.ConfigFile)
	return nil
}
