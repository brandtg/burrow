// Package cli wires the cobra command surface, global flags, and the process
// exit-code mapping.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/brandtg/burrow/internal/distro"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/i18n"
	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/metrics"
	"github.com/spf13/cobra"
)

// GlobalFlags are the flags shared by every command (spec §5.1).
type GlobalFlags struct {
	DryRun   bool
	Yes      bool
	JSON     bool
	Plain    bool
	NoOwner  bool
	Only     []string
	Exclude  []string
	MinSize  string
	Color    string
	NoColor  bool
	Config   string
	LogLevel string
	Verbose  bool
	Quiet    bool
}

// App holds process-wide dependencies so commands are testable.
type App struct {
	In      io.Reader
	Out     io.Writer
	ErrOut  io.Writer
	Version string
	Flags   GlobalFlags

	// Getenv and Geteuid are injectable for tests.
	Getenv  func(string) string
	Geteuid func() int

	// Distro and Adapter override detection for tests and the container
	// matrix.
	Distro  *distro.Info
	Adapter distro.Adapter

	// CollectMetrics overrides the status snapshot source for tests and the
	// command-level golden. It defaults to metrics.Collect.
	CollectMetrics func(context.Context, string) (metrics.Snapshot, error)

	env    *engine.Env
	envErr error
}

// New returns an App writing to out/errOut.
func New(out, errOut io.Writer) *App {
	return &App{In: os.Stdin, Out: out, ErrOut: errOut, Version: journal.Version}
}

// Env resolves and caches the engine environment. Commands that touch the
// filesystem call this; version and completion do not.
func (a *App) Env() (*engine.Env, error) {
	if a.env != nil || a.envErr != nil {
		return a.env, a.envErr
	}
	a.env, a.envErr = engine.Resolve(engine.ResolveOptions{
		Getenv:     a.Getenv,
		Geteuid:    a.Geteuid,
		ConfigPath: a.Flags.Config,
		DryRun:     a.Flags.DryRun,
		Distro:     a.Distro,
		Adapter:    a.Adapter,
	})
	return a.env, a.envErr
}

// Root builds the cobra root command.
func (a *App) Root() *cobra.Command {
	root := &cobra.Command{
		Use:           "burrow",
		Short:         i18n.T(i18n.AppTagline),
		SilenceUsage:  true,
		SilenceErrors: true,
		// Bare `burrow` IS the product: the full clean flow (scan ->
		// grouped preview -> confirm). `burrow clean` runs the same flow.
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runClean(cleanFlags{})
		},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &ExitError{Code: ExitUsage, Err: err}
	})

	pf := root.PersistentFlags()
	pf.BoolVar(&a.Flags.DryRun, "dry-run", false, "never mutate; print the plan and exit")
	pf.BoolVarP(&a.Flags.Yes, "yes", "y", false, "skip the interactive confirm")
	pf.BoolVar(&a.Flags.JSON, "json", false, "machine-readable output")
	pf.BoolVar(&a.Flags.Plain, "plain", false, "non-interactive plain-text output instead of the TUI")
	pf.BoolVar(&a.Flags.NoOwner, "no-owner", false, "skip all owner-command/system acts")
	pf.StringSliceVar(&a.Flags.Only, "only", nil, "comma list of categories or item ids to include")
	pf.StringSliceVar(&a.Flags.Exclude, "exclude", nil, "comma list of categories or item ids to exclude")
	pf.StringVar(&a.Flags.MinSize, "min-size", "0", "only act on items >= size (e.g. 10M, 1G)")
	pf.StringVar(&a.Flags.Color, "color", "auto", "terminal color policy: auto|always|never")
	pf.BoolVar(&a.Flags.NoColor, "no-color", false, "disable color (same as --color=never)")
	pf.StringVar(&a.Flags.Config, "config", "", "override the config file path")
	pf.StringVar(&a.Flags.LogLevel, "log-level", "info", "trace|debug|info|warn|error")
	pf.BoolVarP(&a.Flags.Verbose, "verbose", "v", false, "shorthand for --log-level debug")
	pf.BoolVar(&a.Flags.Quiet, "quiet", false, "only print errors")

	root.AddCommand(
		a.newVersionCmd(),
		a.newConfigCmd(),
		a.newHistoryCmd(),
		a.newAnalyzeCmd(),
		a.newCleanCmd(),
		a.newPurgeCmd(),
		a.newStatusCmd(),
		a.newDocsCmd(root),
	)
	return root
}

// Run executes the CLI with the given arguments (excluding the program name),
// printing any error to ErrOut, and returns the process exit code.
func (a *App) Run(args []string) int {
	root := a.Root()
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	code := ExitCode(err)
	if code != ExitOK {
		fmt.Fprintf(a.ErrOut, "%s\n", err.Error())
	}
	return code
}

// Execute runs the CLI and returns a process exit code.
func Execute() int {
	app := New(os.Stdout, os.Stderr)
	return app.Run(os.Args[1:])
}
