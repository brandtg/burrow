package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/i18n"
	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/purge"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
	"github.com/brandtg/burrow/internal/tui"
	"github.com/spf13/cobra"
)

// purgeOptions are the purge-specific flags (spec §5.4).
type purgeOptions struct {
	paths bool
	age   string
}

func (a *App) newPurgeCmd() *cobra.Command {
	var opts purgeOptions
	cmd := &cobra.Command{
		Use:   "purge",
		Short: "remove rebuildable project artifacts",
		Long: `Find rebuildable build artifacts (node_modules, target, virtualenvs,
__pycache__, ...) under the configured project roots and move them to the
Trash.

Artifacts are kept when they are git-tracked, when git state cannot be
determined, when they contain a git repository, when their activity age cannot
be verified, or when they have seen file activity within the age window. Purged
artifacts are recoverable from the Trash (burrow history --restore). The preview
lists every exact path, and --yes is required when stdout is not a terminal.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := a.Env()
			if err != nil {
				return err
			}
			return a.runPurge(env, opts)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&opts.paths, "paths", false, "print the configured project roots")
	f.StringVar(&opts.age, "age", "", "only artifacts with no activity in this window (default 7d)")
	return cmd
}

func (a *App) runPurge(env *engine.Env, opts purgeOptions) error {
	roots := purgeRoots(env)
	if opts.paths {
		return a.printPurgePaths(env, roots)
	}
	if len(roots) == 0 {
		return &ExitError{Code: ExitUsage, Err: errors.New("no project roots configured; set [purge] paths or BURROW_PURGE_PATHS")}
	}

	maxAge, err := purgeMaxAge(env, opts)
	if err != nil {
		return err
	}
	minSize, err := purgeMinSize(a.Flags.MinSize)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	artifacts, err := purge.Discover(ctx, purge.Options{
		Roots:        roots,
		ArtifactDirs: env.Cfg.Purge.ArtifactDirs,
		MaxAge:       maxAge,
		MaxAgeByType: purgeMaxAgeTypes(env, opts),
		ActiveWithin: purgeActiveWithin(env),
		Warn:         func(msg string) { fmt.Fprintf(a.ErrOut, "purge: %s\n", msg) },
	})
	if err != nil {
		return err
	}
	artifacts = purgeFilterBySize(artifacts, minSize)

	exec := &purge.Executor{
		Validator: purgeValidator(env, roots),
		Trash:     trash.New(env.Paths.TrashDir, env.UID()),
		Journal:   a.purgeJournal(env),
		Command:   "purge",
	}

	if a.Flags.DryRun {
		_, outcomes := executePurge(exec, artifacts, true)
		_ = exec.EndRun(0)
		return a.printPurge(roots, artifacts, outcomes, true)
	}

	if !a.Flags.JSON && !a.Flags.Yes && isTerminal(a.Out) {
		return a.runPurgeTUI(env, artifacts, exec)
	}
	if !a.Flags.Yes {
		return &ExitError{Code: ExitUsage, Err: errors.New("refusing to purge without --yes when not a TTY; re-run with --dry-run to preview")}
	}
	// Purge mutates: --yes must still show exactly what will be removed before
	// doing it (spec §5.1).
	if !a.Flags.JSON {
		if err := a.printPurgeTable(artifacts); err != nil {
			return err
		}
		fmt.Fprintln(a.Out)
	}
	freed, outcomes := executePurge(exec, artifacts, false)
	_ = exec.EndRun(freed)
	return a.printPurgeResult(roots, artifacts, outcomes)
}

func (a *App) runPurgeTUI(env *engine.Env, artifacts []purge.Artifact, exec *purge.Executor) error {
	model := tui.NewPurgeModel(tui.PurgeConfig{
		Artifacts: artifacts,
		Color:     a.colorEnabled(env),
		Executor:  exec,
	})
	return model.Run()
}

// purgeOutcome is the result of acting on one artifact.
type purgeOutcome struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Bytes  int64  `json:"bytes"`
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// purgeJSONItem is one candidate in the stable --json schema.
type purgeJSONItem struct {
	Project   string `json:"project"`
	Path      string `json:"path"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
	SizeKnown bool   `json:"size_known"`
	AgeSecs   int64  `json:"age_seconds"`
	AgeKnown  bool   `json:"age_known"`
	Tracked   bool   `json:"tracked"`
	Kept      bool   `json:"kept"`
	Reason    string `json:"reason,omitempty"`
}

type purgeReport struct {
	Roots      []string        `json:"roots"`
	Candidates []purgeJSONItem `json:"candidates"`
	Results    []purgeOutcome  `json:"results,omitempty"`
	DryRun     bool            `json:"dry_run"`
	Purged     int             `json:"purged"`
	Failed     int             `json:"failed"`
	Freed      int64           `json:"freed_bytes"`
}

// executePurge deletes (or dry-run records) every purgeable artifact and
// returns the freed bytes and per-item outcomes.
func executePurge(exec *purge.Executor, artifacts []purge.Artifact, dryRun bool) (int64, []purgeOutcome) {
	var freed int64
	outcomes := make([]purgeOutcome, 0, len(artifacts))
	for i := range artifacts {
		art := &artifacts[i]
		if art.Kept() {
			continue
		}
		n, err := exec.Delete(art, dryRun)
		outcome := purgeOutcome{Path: art.Path, Type: art.Type, Bytes: art.Size, Status: "ok"}
		switch {
		case dryRun:
			outcome.Status = "dry-run"
		case err != nil:
			outcome.Status = "failed"
			outcome.Reason = err.Error()
		default:
			freed += n
		}
		outcomes = append(outcomes, outcome)
	}
	return freed, outcomes
}

func (a *App) printPurge(roots []string, artifacts []purge.Artifact, outcomes []purgeOutcome, dryRun bool) error {
	if a.Flags.JSON {
		return a.printPurgeJSON(roots, artifacts, outcomes, dryRun)
	}
	return a.printPurgeText(artifacts, outcomes, dryRun)
}

// printPurgeResult renders the outcome of an execution (JSON report or human
// summary). The candidate table is printed by the caller before execution.
func (a *App) printPurgeResult(roots []string, artifacts []purge.Artifact, outcomes []purgeOutcome) error {
	if a.Flags.JSON {
		return a.printPurgeJSON(roots, artifacts, outcomes, false)
	}
	return a.printPurgeSummary(outcomes)
}

func (a *App) printPurgeText(artifacts []purge.Artifact, outcomes []purgeOutcome, dryRun bool) error {
	if len(artifacts) == 0 {
		fmt.Fprintln(a.Out, "nothing to purge")
		return nil
	}
	if err := a.printPurgeTable(artifacts); err != nil {
		return err
	}
	if dryRun {
		fmt.Fprintln(a.Out, "\ndry-run: nothing deleted")
		return nil
	}
	return a.printPurgeSummary(outcomes)
}

// printPurgeTable writes the candidate table (project/artifact/size/age/status/
// path) without a trailing summary.
func (a *App) printPurgeTable(artifacts []purge.Artifact) error {
	tw := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tARTIFACT\tSIZE\tAGE\tSTATUS\tPATH")
	for _, art := range artifacts {
		status := "purge"
		if art.Kept() {
			status = "keep: " + art.KeepReason
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			art.Project, art.Type, purgeSizeLabel(art), purgeAgeLabel(art), status, art.Path)
	}
	return tw.Flush()
}

func (a *App) printPurgeSummary(outcomes []purgeOutcome) error {
	var freed int64
	purged, failed := 0, 0
	for _, o := range outcomes {
		switch o.Status {
		case "ok":
			purged++
			freed += o.Bytes
		case "failed":
			failed++
			fmt.Fprintf(a.ErrOut, "failed %s: %s\n", o.Path, o.Reason)
		}
	}
	fmt.Fprintf(a.Out, "\npurged %d item(s), %s freed\n", purged, config.FormatBytes(freed))
	if failed > 0 {
		fmt.Fprintf(a.Out, "%d item(s) failed (see stderr)\n", failed)
	}
	return nil
}

func (a *App) printPurgeJSON(roots []string, artifacts []purge.Artifact, outcomes []purgeOutcome, dryRun bool) error {
	now := time.Now()
	rep := purgeReport{
		Roots:      roots,
		DryRun:     dryRun,
		Candidates: make([]purgeJSONItem, 0, len(artifacts)),
		Results:    outcomes,
	}
	for _, art := range artifacts {
		item := purgeJSONItem{
			Project:   art.Project,
			Path:      art.Path,
			Type:      art.Type,
			Size:      art.Size,
			SizeKnown: art.SizeKnown,
			Tracked:   art.Tracked,
			Kept:      art.Kept(),
			Reason:    art.KeepReason,
		}
		if art.AgeKnown {
			item.AgeKnown = true
			item.AgeSecs = int64(now.Sub(art.Mtime).Seconds())
		}
		rep.Candidates = append(rep.Candidates, item)
	}
	for _, o := range outcomes {
		switch o.Status {
		case "ok":
			rep.Purged++
			rep.Freed += o.Bytes
		case "failed":
			rep.Failed++
		}
	}
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
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

func (a *App) purgeJournal(env *engine.Env) *journal.Writer {
	w, err := journal.NewWriter(env.Paths.Journal)
	if err != nil {
		fmt.Fprintf(a.ErrOut, "%s\n", i18n.Tf(i18n.WarnNoJournal, err))
		return nil
	}
	return w
}

// purgeRoots expands the configured project roots against the invoking user's
// home, resolves symlinks, and de-duplicates them. Discovery reports artifacts
// at their physical path, so the deletion allow-list must use the same resolved
// roots or a symlinked project root (for example ~/code -> /data/code) would be
// rejected as outside the allow-list. A root that cannot be resolved (for
// example, one that does not exist yet) keeps its lexical form; discovery will
// warn about it and skip it.
func purgeRoots(env *engine.Env) []string {
	var roots []string
	seen := map[string]bool{}
	for _, p := range env.Cfg.Purge.Paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		abs := config.ExpandHome(p, env.Home)
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(env.Home, abs)
		}
		abs = filepath.Clean(abs)
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = filepath.Clean(resolved)
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		roots = append(roots, abs)
	}
	return roots
}

// purgeValidator binds the deletion validator to the configured roots, so a
// purge can only ever delete inside a project root (invariant I3).
func purgeValidator(env *engine.Env, roots []string) *safe.Validator {
	v := safe.NewValidator(env.UID(), env.Cfg.Clean.ProtectedPaths...)
	v.Roots = roots
	v.RequireRoot = true
	return v
}

func purgeMaxAge(env *engine.Env, opts purgeOptions) (time.Duration, error) {
	age := env.Cfg.Purge.MaxAge
	if opts.age != "" {
		age = opts.age
	}
	d, err := config.ParseDuration(age)
	if err != nil {
		return 0, &ExitError{Code: ExitUsage, Err: err}
	}
	return d, nil
}

// purgeMaxAgeTypes converts the per-artifact windows from config. An explicit
// --age overrides the whole policy, so the per-type map is dropped.
func purgeMaxAgeTypes(env *engine.Env, opts purgeOptions) map[string]time.Duration {
	if opts.age != "" || env.Cfg == nil || len(env.Cfg.Purge.MaxAgeByType) == 0 {
		return nil
	}
	out := make(map[string]time.Duration, len(env.Cfg.Purge.MaxAgeByType))
	for pattern, raw := range env.Cfg.Purge.MaxAgeByType {
		d, err := config.ParseDuration(raw)
		if err != nil {
			continue
		}
		out[pattern] = d
	}
	return out
}

// purgeActiveWithin returns the active-project guard window from config.
func purgeActiveWithin(env *engine.Env) time.Duration {
	if env.Cfg == nil {
		return 0
	}
	d, err := config.ParseDuration(env.Cfg.Purge.ActiveWithin)
	if err != nil {
		return 0
	}
	return d
}

func purgeMinSize(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	n, err := config.ParseBytes(s)
	if err != nil {
		return 0, &ExitError{Code: ExitUsage, Err: err}
	}
	return n, nil
}

func purgeFilterBySize(artifacts []purge.Artifact, min int64) []purge.Artifact {
	if min <= 0 {
		return artifacts
	}
	out := artifacts[:0:0]
	for _, a := range artifacts {
		if a.SizeKnown && a.Size >= min {
			out = append(out, a)
		}
	}
	return out
}

func purgeSizeLabel(a purge.Artifact) string {
	if !a.SizeKnown {
		return "unknown"
	}
	return config.FormatBytes(a.Size)
}

func purgeAgeLabel(a purge.Artifact) string {
	if !a.AgeKnown {
		return "unknown"
	}
	age := time.Since(a.Mtime)
	if age < 0 {
		age = 0
	}
	days := int(age.Hours() / 24)
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dh", int(age.Hours()))
}
