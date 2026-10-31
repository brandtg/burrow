package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/scan"
	"github.com/brandtg/burrow/internal/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// analyzeOptions are the analyze-specific flags.
type analyzeOptions struct {
	apparent    bool
	oneFS       bool
	crossMounts bool
	noHidden    bool
	depth       int
	top         int
}

func (a *App) newAnalyzeCmd() *cobra.Command {
	var opts analyzeOptions
	cmd := &cobra.Command{
		Use:   "analyze [path...]",
		Short: "read-only disk explorer",
		Long: `Explore where disk space went.

Read-only: navigate the tree with the arrow keys. The only way to delete
things in burrow is the clean flow's review screen.

The --json schema is stable:
  {"path":"...","total_size":N,"total_files":N,
   "entries":[{"name":"...","path":"...","size":N,"is_dir":true}],
   "largest":[...],"scan_status":"complete|partial|unavailable"}

When stdout is not a terminal (or --json is set), analyze behaves like
--json and never waits for input. --json accepts a single path.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := a.Env()
			if err != nil {
				return err
			}
			// Precedence: an explicit --cross-mounts disables the
			// boundary, an explicit -x/--one-file-system enables it, otherwise
			// the config value applies. The boundary is on by default so a
			// scan of a path on one drive does not silently absorb other
			// mounted filesystems.
			switch {
			case cmd.Flags().Changed("cross-mounts") && opts.crossMounts:
				opts.oneFS = false
			case cmd.Flags().Changed("one-file-system"):
				opts.oneFS = true
			default:
				opts.oneFS = env.Cfg.Analyze.OneFileSystem
			}
			return a.runAnalyze(env, args, opts)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&opts.apparent, "apparent", "a", false, "show apparent size instead of on-disk usage")
	f.BoolVarP(&opts.oneFS, "one-file-system", "x", false, "do not descend across mount points (default; btrfs subvolumes on the same device stay in scope)")
	f.BoolVar(&opts.crossMounts, "cross-mounts", false, "descend into nested mounts on other filesystems")
	f.BoolVarP(&opts.noHidden, "no-hidden", "H", false, "skip dotfiles")
	f.IntVar(&opts.depth, "depth", 0, "non-interactive depth (with --json); 0 = unlimited")
	f.IntVarP(&opts.top, "top", "t", 10, "non-interactive top-N largest entries (with --json)")
	return cmd
}

func (a *App) runAnalyze(env *engine.Env, args []string, opts analyzeOptions) error {
	paths := args
	if len(paths) == 0 {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		paths = []string{cwd}
	}

	interactive := !a.Flags.JSON && isTerminal(a.Out)
	if !interactive && len(paths) > 1 {
		return &ExitError{Code: ExitUsage, Err: errors.New("--json accepts a single path")}
	}

	scanOpts := scan.Options{
		Ignore:        env.Cfg.Analyze.Ignore,
		OneFileSystem: opts.oneFS,
		NoHidden:      opts.noHidden,
		Apparent:      opts.apparent,
		Depth:         opts.depth,
	}

	if interactive {
		return a.runAnalyzeTUI(env, paths, scanOpts, opts)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return a.printAnalyzeJSON(ctx, paths[0], scanOpts, opts)
}

// scanRoots scans one or more paths. Multiple roots are folded under a
// synthetic parent so the interactive tree has a single root.
func (a *App) scanRoots(ctx context.Context, paths []string, opts scan.Options) (*scan.Node, error) {
	if len(paths) == 1 {
		return scan.Default().Scan(ctx, paths[0], opts)
	}
	synth := &scan.Node{Name: "analyze", IsDir: true, SizeKnown: true}
	for _, p := range paths {
		n, err := scan.Default().Scan(ctx, p, opts)
		if err != nil {
			return nil, err
		}
		synth.Children = append(synth.Children, n)
		synth.Size += n.Size
		synth.Apparent += n.Apparent
		synth.ItemCount += n.ItemCount
		synth.SkippedMounts = append(synth.SkippedMounts, n.SkippedMounts...)
	}
	sort.SliceStable(synth.Children, func(i, j int) bool {
		return synth.Children[i].Size > synth.Children[j].Size
	})
	return synth, nil
}

func (a *App) runAnalyzeTUI(env *engine.Env, paths []string, scanOpts scan.Options, opts analyzeOptions) error {
	model := tui.NewAnalyzeModel(tui.AnalyzeConfig{
		// Scan in the background so the TUI can show progress instead of
		// freezing on a cold cache (the model renders a loading view).
		Load: func(ctx context.Context, progress func(scan.Progress)) (*scan.Node, error) {
			scanOpts.Progress = progress
			return a.scanRoots(ctx, paths, scanOpts)
		},
		Apparent: opts.apparent,
		Color:    a.colorEnabled(env),
	})
	return model.Run()
}

// jsonEntry is one entry in the stable --json schema.
type jsonEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

// jsonSkippedMount is a nested mount the one-file-system boundary refused to
// cross. It is reported so a subtree on another drive is visible rather than
// silently absent from the totals.
type jsonSkippedMount struct {
	Path   string `json:"path"`
	Source string `json:"source,omitempty"`
	Fstype string `json:"fstype,omitempty"`
}

// jsonReport is the stable --json schema.
type jsonReport struct {
	Path          string             `json:"path"`
	TotalSize     int64              `json:"total_size"`
	TotalFiles    int64              `json:"total_files"`
	Entries       []jsonEntry        `json:"entries"`
	Largest       []jsonEntry        `json:"largest"`
	ScanStatus    string             `json:"scan_status"`
	SkippedMounts []jsonSkippedMount `json:"skipped_mounts,omitempty"`
}

func (a *App) printAnalyzeJSON(ctx context.Context, path string, opts scan.Options, aopts analyzeOptions) error {
	root, err := scan.Default().Scan(ctx, path, opts)
	if err != nil {
		return err
	}
	report := jsonReport{
		Path:          root.Path,
		TotalSize:     root.DisplaySize(aopts.apparent),
		TotalFiles:    scan.FileCount(root),
		Entries:       collectEntries(root, entryDepth(aopts.depth), aopts.apparent),
		Largest:       largest(root, aopts.top, aopts.apparent),
		ScanStatus:    scan.Status(root),
		SkippedMounts: jsonSkippedMounts(root.SkippedMounts),
	}
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// entryDepth resolves the depth used for the JSON "entries" list. Unlimited
// scans default to direct children so the payload stays bounded.
func entryDepth(depth int) int {
	if depth <= 0 {
		return 1
	}
	return depth
}

// collectEntries flattens the tree up to depth levels below root.
func collectEntries(root *scan.Node, depth int, apparent bool) []jsonEntry {
	out := []jsonEntry{}
	var walk func(n *scan.Node, level int)
	walk = func(n *scan.Node, level int) {
		if level > depth {
			return
		}
		for _, c := range n.Children {
			out = append(out, jsonEntry{Name: c.Name, Path: c.Path, Size: c.DisplaySize(apparent), IsDir: c.IsDir})
			walk(c, level+1)
		}
	}
	walk(root, 1)
	return out
}

// largest returns the top-N non-root nodes by displayed size.
func largest(root *scan.Node, top int, apparent bool) []jsonEntry {
	nodes := scan.Flatten(root)
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].DisplaySize(apparent) > nodes[j].DisplaySize(apparent)
	})
	if top < 0 {
		top = 0
	}
	if top > len(nodes) {
		top = len(nodes)
	}
	out := make([]jsonEntry, 0, top)
	for _, n := range nodes[:top] {
		out = append(out, jsonEntry{Name: n.Name, Path: n.Path, Size: n.DisplaySize(apparent), IsDir: n.IsDir})
	}
	return out
}

// jsonSkippedMounts converts scan-level skipped mounts into the JSON schema.
func jsonSkippedMounts(skips []scan.SkippedMount) []jsonSkippedMount {
	if len(skips) == 0 {
		return nil
	}
	out := make([]jsonSkippedMount, 0, len(skips))
	for _, s := range skips {
		out = append(out, jsonSkippedMount{Path: s.Path, Source: s.Source, Fstype: s.Fstype})
	}
	return out
}

func (a *App) colorEnabled(env *engine.Env) bool {
	if a.Flags.NoColor {
		return false
	}
	switch a.Flags.Color {
	case "always":
		return true
	case "never":
		return false
	}
	if env != nil && env.Cfg != nil {
		switch env.Cfg.General.Color {
		case "always":
			return true
		case "never":
			return false
		}
	}
	return isTerminal(a.Out)
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// readerIsTerminal reports whether r is an interactive terminal.
func readerIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
