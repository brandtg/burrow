package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/brandtg/burrow/internal/catalog"
	"github.com/brandtg/burrow/internal/catalog/containers"
	"github.com/brandtg/burrow/internal/catalog/desktoppkgs"
	"github.com/brandtg/burrow/internal/catalog/dev"
	"github.com/brandtg/burrow/internal/catalog/downloads"
	"github.com/brandtg/burrow/internal/catalog/projects"
	"github.com/brandtg/burrow/internal/catalog/system"
	"github.com/brandtg/burrow/internal/catalog/usercache"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/tui"
	"github.com/brandtg/burrow/internal/whitelist"
	"github.com/spf13/cobra"
)

type cleanFlags struct {
	all        bool
	whitelist  bool
	age        string
	minSizeSet bool
}

func (a *App) newCleanCmd() *cobra.Command {
	var f cleanFlags
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "categorized cleaner (preview -> confirm -> execute)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f.minSizeSet = cmd.Flags().Changed("min-size")
			return a.runClean(f)
		},
	}
	cmd.Flags().BoolVar(&f.all, "all", false, "include every category (still respects whitelist and guards)")
	cmd.Flags().BoolVar(&f.whitelist, "whitelist", false, "manage the whitelist")
	cmd.Flags().StringVar(&f.age, "age", "", "only artifacts with no activity in this window (default 7d)")
	a.addSelectionFlags(cmd)
	return cmd
}

func (a *App) runClean(f cleanFlags) error {
	env, err := a.Env()
	if err != nil {
		// A bad config path, an invalid config, or a root-without-SUDO_USER
		// refusal is an invocation problem, not an operational failure.
		return &ExitError{Code: ExitUsage, Err: err}
	}
	if f.whitelist {
		return a.runWhitelist(env)
	}

	minSize, err := a.resolveMinSize(env, f.minSizeSet)
	if err != nil {
		return &ExitError{Code: ExitUsage, Err: err}
	}
	if f.age != "" {
		if _, err := config.ParseDuration(f.age); err != nil {
			return &ExitError{Code: ExitUsage, Err: err}
		}
		// An explicit --age overrides the configured inactivity window.
		env.Cfg.Purge.MaxAge = f.age
	}

	reg := catalog.NewRegistry(system.New(), projects.New(), dev.New(), usercache.New(), downloads.New(), containers.New(), desktoppkgs.New())
	cats := a.selectCategories(env, reg, f.all)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	progress := newScanProgress(a.ErrOut, !a.Flags.JSON)
	defer progress.stop()

	plan, err := engine.Build(ctx, env, engine.Options{
		Command:    "clean",
		Categories: cats,
		Only:       a.Flags.Only,
		Exclude:    a.Flags.Exclude,
		MinSize:    minSize,
		NoOwner:    a.Flags.NoOwner,
		Progress:   progress.category,
	})
	if err != nil {
		return err
	}
	// The scan is over: clear the progress line before anything is written to
	// stdout or before the alternate screen opens.
	progress.stop()
	engine.ApplyHooks(cats, plan)
	// Warnings are buffered. Printed before the alternate screen they are
	// invisible until the TUI exits, so they are flushed afterwards instead.
	warnings := plan.Warnings

	platform := a.platformHeader(env)
	color := a.colorEnabled(env)
	jsonOut := a.Flags.JSON
	tty := readerIsTerminal(a.In) && isTerminal(a.Out)

	// --dry-run is the stronger intent: --yes cannot turn it into an
	// execution, so say so rather than silently dropping the flag.
	if env.DryRun && a.Flags.Yes {
		a.printNote(jsonOut, "note: --dry-run wins; --yes ignored")
	}

	// The TUI is the default view on a terminal, including --dry-run (where it
	// is read-only). --plain, --json, --yes, or a non-TTY stdin/stdout select
	// the non-interactive path.
	interactive := tty && !jsonOut && !a.Flags.Plain && !a.Flags.Yes
	switch {
	case jsonOut:
		a.printWarnings(warnings)
		if env.DryRun || !a.Flags.Yes {
			// A scripted run that cannot execute prints the plan and stops.
			// Nothing was changed, so dry_run is true.
			return a.cleanJSON(plan, nil, true)
		}
	case interactive:
		confirmed, aborted, err := tui.RunClean(tui.CleanConfig{
			Plan:      plan,
			Platform:  platform,
			Color:     color,
			DryRun:    env.DryRun,
			Whitelist: a.whitelistManager(env),
		})
		a.printWarnings(warnings)
		if err != nil {
			return err
		}
		if aborted || !confirmed {
			return nil
		}
		if env.DryRun {
			// The read-only TUI showed the plan; nothing may run.
			return nil
		}
	default:
		a.printWarnings(warnings)
		tui.RenderPreview(a.Out, plan, platform, color)
		if env.DryRun || !a.Flags.Yes {
			if env.DryRun {
				fmt.Fprintln(a.Out, "nothing was changed (dry run).")
			} else {
				fmt.Fprintln(a.Out, "nothing was changed; re-run with --yes to execute")
			}
			return nil
		}
		// --yes executes: the preview was just shown, so go straight to the
		// one-line handoff and the per-item progress below.
		fmt.Fprintf(a.Out, "executing %d items\u2026\n", len(plan.Selected()))
	}

	// Reconcile again after an interactive selection: a category may need to
	// drop items the user selected that a superset item now covers.
	engine.ApplyHooks(cats, plan)

	jw, jerr := journal.NewWriter(env.Paths.Journal)
	if jerr != nil {
		fmt.Fprintf(a.ErrOut, "warning: journal unavailable: %v\n", jerr)
		jw = nil
	}
	sum := engine.ExecuteWithProgress(ctx, env, plan, jw, a.itemProgress)

	if jsonOut {
		if err := a.cleanJSON(plan, sum, false); err != nil {
			return err
		}
		return cleanExit(sum)
	}
	tui.RenderSummary(a.Out, sum, color)
	err = cleanExit(sum)
	if err != nil && ExitCode(err) == ExitGuard {
		fmt.Fprintf(a.Out, "note: %d items were skipped by a safety guard; nothing unsafe ran\n", sum.GuardStops)
	}
	return err
}

// printWarnings flushes the buffered discovery warnings to stderr.
func (a *App) printWarnings(warnings []string) {
	for _, w := range warnings {
		fmt.Fprintf(a.ErrOut, "warning: %s\n", w)
	}
}

// printNote writes an invocation notice. --json keeps stdout a single clean
// document, so the notice moves to stderr there.
func (a *App) printNote(jsonOut bool, note string) {
	if jsonOut {
		fmt.Fprintln(a.ErrOut, note)
		return
	}
	fmt.Fprintln(a.Out, note)
}

// itemProgress prints one line per item as it completes during execution.
// Under --json stdout must stay a single document, so there is no progress.
func (a *App) itemProgress(it *engine.PlannedItem, _ engine.Result) {
	if a.Flags.JSON {
		return
	}
	status := "ok"
	switch it.Status {
	case engine.StatusSkipped, engine.StatusDryRun:
		status = "skip"
	case engine.StatusFailed:
		status = "fail"
	}
	line := fmt.Sprintf("  %s %s (%s)", status, it.Target.Display, it.Target.Recovery.String())
	if it.Reason != "" {
		line += " \u2014 " + it.Reason
	}
	fmt.Fprintln(a.ErrOut, line)
}

// scanProgress is the dependency-free stderr reporter shown while engine.Build
// runs. It is inert unless stderr is a terminal, so piped and --json runs stay
// silent and scriptable.
type scanProgress struct {
	w       io.Writer
	enabled bool
	start   time.Time
	mu      sync.Mutex
	stopCh  chan struct{}
	doneCh  chan struct{}
	once    sync.Once
}

func newScanProgress(w io.Writer, enabled bool) *scanProgress {
	p := &scanProgress{w: w, enabled: enabled && isTerminal(w), start: time.Now()}
	if !p.enabled {
		return p
	}
	p.stopCh = make(chan struct{})
	p.doneCh = make(chan struct{})
	go func() {
		defer close(p.doneCh)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-p.stopCh:
				return
			case <-tick.C:
				p.mu.Lock()
				fmt.Fprintf(p.w, "\rscanning\u2026 %.1fs", time.Since(p.start).Seconds())
				p.mu.Unlock()
			}
		}
	}()
	return p
}

// category is the engine.Options.Progress callback: one line per category as
// its discovery lands.
func (p *scanProgress) category(id string, targets int) {
	if p == nil || !p.enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.w, "\r\033[K  %s: %d items\n", id, targets)
}

// stop halts the ticker and clears the line it drew. It is idempotent.
func (p *scanProgress) stop() {
	if p == nil || !p.enabled {
		return
	}
	p.once.Do(func() {
		close(p.stopCh)
		<-p.doneCh
		p.mu.Lock()
		fmt.Fprint(p.w, "\r\033[K")
		p.mu.Unlock()
	})
}

// whitelistManager loads the whitelist for the interactive keep key. A load
// failure returns nil so the key is disabled rather than risking a clobbering
// save.
func (a *App) whitelistManager(env *engine.Env) *whitelist.Manager {
	if env == nil || env.Paths.Whitelist == "" {
		return nil
	}
	mgr := whitelist.NewManager(env.Paths.Whitelist)
	if err := mgr.Load(); err != nil {
		return nil
	}
	return mgr
}

// runWhitelist loads the whitelist manager and either runs the interactive
// add/remove loop (on a terminal) or prints the current entries.
func (a *App) runWhitelist(env *engine.Env) error {
	mgr := whitelist.NewManager(env.Paths.Whitelist)
	if err := mgr.Load(); err != nil {
		return err
	}
	if !readerIsTerminal(a.In) || !isTerminal(a.Out) {
		return a.printWhitelistEntries(mgr.Entries())
	}
	return a.interactiveWhitelist(mgr)
}

func (a *App) interactiveWhitelist(mgr *whitelist.Manager) error {
	fmt.Fprintf(a.Out, "whitelist: %s\n", mgr.Path())
	in := a.In
	if in == nil {
		in = os.Stdin
	}
	sc := bufio.NewScanner(in)
	for {
		if err := a.printWhitelistEntries(mgr.Entries()); err != nil {
			return err
		}
		fmt.Fprint(a.Out, "add <path> | remove <path> | list | quit\n> ")
		if !sc.Scan() {
			return sc.Err()
		}
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "q", "quit", "exit":
			return nil
		case "list", "ls":
			continue
		case "add", "remove", "rm":
			if len(fields) < 2 {
				fmt.Fprintf(a.ErrOut, "usage: %s <absolute path>\n", fields[0])
				continue
			}
			target := strings.Join(fields[1:], " ")
			var changed bool
			var err error
			if fields[0] == "add" {
				changed, err = mgr.Add(target)
			} else {
				changed, err = mgr.Remove(target)
			}
			if err != nil {
				fmt.Fprintf(a.ErrOut, "%v\n", err)
				continue
			}
			if !changed {
				fmt.Fprintln(a.Out, "no change")
				continue
			}
			if err := mgr.Save(); err != nil {
				return err
			}
			fmt.Fprintln(a.Out, "saved")
		default:
			fmt.Fprintf(a.Out, "unknown command %q\n", fields[0])
		}
	}
}

func (a *App) printWhitelistEntries(entries []string) error {
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

// resolveMinSize resolves the size floor. An explicit --min-size wins, even
// when it is "0" (which turns the configured floor off); otherwise the config
// value applies.
func (a *App) resolveMinSize(env *engine.Env, set bool) (int64, error) {
	raw := a.Flags.MinSize
	if !set {
		raw = ""
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
	ID         string   `json:"id"`
	Category   string   `json:"category"`
	Display    string   `json:"display"`
	Path       string   `json:"path,omitempty"`
	Size       int64    `json:"size"`
	SizeKnown  bool     `json:"size_known"`
	Recovery   string   `json:"recovery"`
	NeedsRoot  bool     `json:"needs_root"`
	Risk       string   `json:"risk"`
	Regen      string   `json:"regen"`
	DefaultOn  bool     `json:"default_on"`
	Selected   bool     `json:"selected"`
	Kept       bool     `json:"kept,omitempty"`
	Status     string   `json:"status"`
	Reason     string   `json:"reason,omitempty"`
	Argv       []string `json:"argv,omitempty"`
	Group      string   `json:"group,omitempty"`
	GroupTitle string   `json:"group_title,omitempty"`
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
			ID:         it.Target.ID,
			Category:   it.Target.Category,
			Display:    it.Target.Display,
			Path:       it.Target.Path,
			Size:       it.Target.SizeBytes,
			SizeKnown:  it.Target.SizeKnown,
			Recovery:   it.Target.Recovery.String(),
			NeedsRoot:  it.Target.NeedsRoot,
			Risk:       it.Target.Risk.String(),
			Regen:      it.Target.Regen.String(),
			DefaultOn:  it.Target.DefaultOn,
			Selected:   it.Selected,
			Kept:       it.Kept,
			Status:     it.Status,
			Reason:     it.Reason,
			Argv:       it.Argv,
			Group:      it.Target.Group,
			GroupTitle: it.Target.GroupTitle,
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
