package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/brandtg/burrow/internal/catalog"
	"github.com/brandtg/burrow/internal/catalog/system"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/tui"
	"github.com/spf13/cobra"
)

type cleanFlags struct {
	all       bool
	noOwner   bool
	permanent bool
	whitelist bool
}

func (a *App) newCleanCmd() *cobra.Command {
	var f cleanFlags
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "categorized cleaner (preview -> confirm -> execute)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runClean(f)
		},
	}
	cmd.Flags().BoolVar(&f.all, "all", false, "include every category (still respects whitelist and guards)")
	cmd.Flags().BoolVar(&f.noOwner, "no-owner", false, "skip all owner-command/system acts")
	cmd.Flags().BoolVar(&f.permanent, "permanent", false, "delete trash-contract items permanently instead of trashing")
	cmd.Flags().BoolVar(&f.whitelist, "whitelist", false, "manage the whitelist")
	return cmd
}

func (a *App) runClean(f cleanFlags) error {
	env, err := a.Env()
	if err != nil {
		return err
	}
	if f.whitelist {
		return a.printWhitelist(env)
	}

	minSize, err := a.resolveMinSize(env)
	if err != nil {
		return &ExitError{Code: ExitUsage, Err: err}
	}

	reg := catalog.NewRegistry(system.New())
	cats := a.selectCategories(env, reg, f.all)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	plan, err := engine.Build(ctx, env, engine.Options{
		Command:    "clean",
		Categories: cats,
		Only:       a.Flags.Only,
		Exclude:    a.Flags.Exclude,
		MinSize:    minSize,
		NoOwner:    f.noOwner,
		Permanent:  f.permanent,
	})
	if err != nil {
		return err
	}
	for _, w := range plan.Warnings {
		fmt.Fprintf(a.ErrOut, "warning: %s\n", w)
	}

	platform := a.platformHeader(env)
	color := a.colorEnabled(env)

	if a.Flags.JSON && env.DryRun {
		return a.cleanJSON(plan, nil, true)
	}

	interactive := isTerminal(a.Out) && !a.Flags.Yes && !a.Flags.DryRun
	if interactive {
		confirmed, aborted, err := tui.RunInteractive(plan, color)
		if err != nil {
			return err
		}
		if aborted || !confirmed {
			return nil
		}
	} else if !a.Flags.JSON {
		tui.RenderPreview(a.Out, plan, platform, color)
		tui.RenderReview(a.Out, plan)
	}

	if env.DryRun {
		return nil
	}
	if !interactive && !a.Flags.Yes {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("refusing to execute without confirmation; pass --yes or --dry-run")}
	}

	jw, jerr := journal.NewWriter(env.Paths.Journal)
	if jerr != nil {
		fmt.Fprintf(a.ErrOut, "warning: journal unavailable: %v\n", jerr)
		jw = nil
	}
	sum := engine.Execute(ctx, env, plan, jw)

	if a.Flags.JSON {
		if err := a.cleanJSON(plan, sum, false); err != nil {
			return err
		}
		return cleanExit(sum)
	}
	tui.RenderSummary(a.Out, sum, color)
	return cleanExit(sum)
}

func (a *App) printWhitelist(env *engine.Env) error {
	entries := env.Whitelist.Entries()
	if a.Flags.JSON {
		enc := json.NewEncoder(a.Out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"whitelist": entries})
	}
	if len(entries) == 0 {
		fmt.Fprintln(a.Out, "whitelist is empty")
		return nil
	}
	for _, e := range entries {
		fmt.Fprintln(a.Out, e)
	}
	return nil
}

func (a *App) resolveMinSize(env *engine.Env) (int64, error) {
	raw := a.Flags.MinSize
	if raw == "" || raw == "0" {
		if env.Cfg != nil {
			raw = env.Cfg.Clean.MinSize
		}
	}
	return config.ParseBytes(raw)
}

// selectCategories picks the categories to discover. --all and --only bypass
// the config default_categories list; otherwise the config list is honored
// when it names a registered category, and every registered category is used
// when it does not.
func (a *App) selectCategories(env *engine.Env, reg *catalog.Registry, all bool) []engine.Category {
	all_cats := reg.Categories()
	if all || len(a.Flags.Only) > 0 {
		return all_cats
	}
	defaults := map[string]bool{}
	if env.Cfg != nil {
		for _, id := range env.Cfg.Clean.DefaultCategories {
			defaults[id] = true
		}
	}
	var out []engine.Category
	for _, c := range all_cats {
		if defaults[c.ID()] {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		return all_cats
	}
	return out
}

func (a *App) platformHeader(env *engine.Env) string {
	name := env.Distro.PrettyName
	if name == "" {
		name = env.Distro.ID
	}
	if name == "" {
		name = "unknown"
	}
	return fmt.Sprintf("%s \u00b7 %s \u00b7 %s free", name, env.Adapter.ID(), config.FormatBytes(int64(engine.FreeBytes("/"))))
}

func cleanExit(sum *engine.Summary) error {
	switch {
	case sum.Interrupted:
		return &ExitError{Code: ExitInterrupt}
	case sum.Failed > 0:
		return &ExitError{Code: ExitPartial}
	case sum.OK == 0 && sum.Timeouts > 0 && sum.GuardStops == 0:
		return &ExitError{Code: ExitTimeout}
	case sum.OK == 0 && sum.GuardStops > 0 && sum.GuardStops == sum.Skipped:
		return &ExitError{Code: ExitGuard}
	}
	return nil
}

type cleanItemJSON struct {
	ID        string   `json:"id"`
	Category  string   `json:"category"`
	Display   string   `json:"display"`
	Path      string   `json:"path,omitempty"`
	Size      int64    `json:"size"`
	SizeKnown bool     `json:"size_known"`
	Recovery  string   `json:"recovery"`
	NeedsRoot bool     `json:"needs_root"`
	Risk      string   `json:"risk"`
	DefaultOn bool     `json:"default_on"`
	Selected  bool     `json:"selected"`
	Status    string   `json:"status"`
	Reason    string   `json:"reason,omitempty"`
	Argv      []string `json:"argv,omitempty"`
}

type cleanJSONDoc struct {
	RunID   string          `json:"run_id"`
	Command string          `json:"command"`
	DryRun  bool            `json:"dry_run"`
	Items   []cleanItemJSON `json:"items"`
	Totals  engine.Totals   `json:"totals"`
	Summary *cleanSummary   `json:"summary,omitempty"`
}

type cleanSummary struct {
	OK          int    `json:"ok"`
	Skipped     int    `json:"skipped"`
	Failed      int    `json:"failed"`
	GuardStops  int    `json:"guard_stops"`
	Timeouts    int    `json:"timeouts"`
	Freed       int64  `json:"freed"`
	FreedKnown  bool   `json:"freed_known"`
	DFBefore    uint64 `json:"df_before"`
	DFAfter     uint64 `json:"df_after"`
	JournalPath string `json:"journal_path,omitempty"`
	Interrupted bool   `json:"interrupted"`
}

func (a *App) cleanJSON(plan *engine.Plan, sum *engine.Summary, dryRun bool) error {
	doc := cleanJSONDoc{
		RunID:   plan.RunID,
		Command: plan.Command,
		DryRun:  dryRun,
		Totals:  plan.SelectedTotals(),
	}
	for _, it := range plan.Items {
		doc.Items = append(doc.Items, cleanItemJSON{
			ID:        it.Target.ID,
			Category:  it.Target.Category,
			Display:   it.Target.Display,
			Path:      it.Target.Path,
			Size:      it.Target.SizeBytes,
			SizeKnown: it.Target.SizeKnown,
			Recovery:  it.Target.Recovery.String(),
			NeedsRoot: it.Target.NeedsRoot,
			Risk:      it.Target.Risk.String(),
			DefaultOn: it.Target.DefaultOn,
			Selected:  it.Selected,
			Status:    it.Status,
			Reason:    it.Reason,
			Argv:      it.Argv,
		})
	}
	if sum != nil {
		doc.Summary = &cleanSummary{
			OK: sum.OK, Skipped: sum.Skipped, Failed: sum.Failed,
			GuardStops: sum.GuardStops, Timeouts: sum.Timeouts,
			Freed: sum.Freed, FreedKnown: sum.FreedKnown,
			DFBefore: sum.DFBefore, DFAfter: sum.DFAfter,
			JournalPath: sum.JournalPath, Interrupted: sum.Interrupted,
		}
	}
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}
