package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/whitelist"
)

// PlannedItem is one target in a plan plus the state the UI and executor track.
type PlannedItem struct {
	Target        Target
	CategoryTitle string
	CategoryNote  string
	Selected      bool
	Status        string
	Reason        string
	Freed         int64
	Argv          []string
	GuardStop     bool
	TimedOut      bool
	// Kept is true when the user pressed keep in the interactive preview. The
	// item stays in the plan for transparency but is never executed.
	Kept bool
}

// Plan is the read-only result of pipeline steps 2-5 (discover, measure,
// filter, plan). Nothing here mutates the filesystem.
type Plan struct {
	RunID    string
	Command  string
	Items    []*PlannedItem
	Warnings []string

	// hooksApplied records that ApplyHooks ran, so a second call neither
	// re-reconciles nor duplicates category warnings.
	hooksApplied bool
}

// Totals summarizes a set of items.
type Totals struct {
	Count   int   `json:"count"`
	Bytes   int64 `json:"bytes"`
	Unknown int   `json:"unknown"`
}

// ItemGroup is an optional presentation sub-group within a category. It is
// populated from Target.Group; when every item in a category has an empty Group
// the category has a single implicit group with an empty ID.
type ItemGroup struct {
	ID      string
	Title   string
	Items   []*PlannedItem
	Bytes   int64
	Unknown int
}

// CategoryGroup is a category's items for the preview.
type CategoryGroup struct {
	ID      string
	Title   string
	Note    string
	Items   []*PlannedItem
	Groups  []ItemGroup
	Bytes   int64
	Unknown int
}

// Options configures Build.
type Options struct {
	Command         string
	Categories      []Category
	Only            []string
	Exclude         []string
	MinSize         int64
	DiscoverTimeout time.Duration
	// NoOwner leaves owner-contract items unselected (still shown).
	NoOwner bool
}

// Selected returns the items marked for execution.
func (p *Plan) Selected() []*PlannedItem {
	var out []*PlannedItem
	for _, it := range p.Items {
		if it.Selected && !it.Kept {
			out = append(out, it)
		}
	}
	return out
}

// TotalsOf sums a set of items.
func TotalsOf(items []*PlannedItem) Totals {
	var t Totals
	for _, it := range items {
		t.Count++
		if it.Target.SizeKnown && it.Target.SizeBytes >= 0 {
			t.Bytes += it.Target.SizeBytes
		} else {
			t.Unknown++
		}
	}
	return t
}

// SelectedTotals sums the selected items.
func (p *Plan) SelectedTotals() Totals { return TotalsOf(p.Selected()) }

// GroupedTree returns items as category -> optional group -> item, preserving
// plan order at every level. The grouping is presentation-only; it never
// changes which targets execute or how.
func (p *Plan) GroupedTree() []CategoryGroup {
	catIndex := map[string]int{}
	groupIndex := map[string]map[string]int{}
	var groups []CategoryGroup
	for _, it := range p.Items {
		ci, ok := catIndex[it.Target.Category]
		if !ok {
			catIndex[it.Target.Category] = len(groups)
			groups = append(groups, CategoryGroup{
				ID:    it.Target.Category,
				Title: it.CategoryTitle,
				Note:  it.CategoryNote,
			})
			ci = len(groups) - 1
		}
		cg := &groups[ci]
		cg.Items = append(cg.Items, it)

		if groupIndex[it.Target.Category] == nil {
			groupIndex[it.Target.Category] = map[string]int{}
		}
		gi, ok := groupIndex[it.Target.Category][it.Target.Group]
		if !ok {
			groupIndex[it.Target.Category][it.Target.Group] = len(cg.Groups)
			title := it.Target.GroupTitle
			if title == "" {
				title = it.CategoryTitle
			}
			cg.Groups = append(cg.Groups, ItemGroup{ID: it.Target.Group, Title: title})
			gi = len(cg.Groups) - 1
		}
		ig := &cg.Groups[gi]
		ig.Items = append(ig.Items, it)

		if it.Target.SizeKnown && it.Target.SizeBytes >= 0 {
			cg.Bytes += it.Target.SizeBytes
			ig.Bytes += it.Target.SizeBytes
		} else {
			cg.Unknown++
			ig.Unknown++
		}
	}
	return groups
}

// Build runs steps 2-5: discover (parallel, per-category timeout), measure,
// filter, and plan. Discovery is limited to categories --only can match, so a
// narrow selector does not pay for scanning unrelated roots. It never mutates
// the filesystem (invariant I1).
func Build(ctx context.Context, env *Env, opts Options) (*Plan, error) {
	if opts.Command == "" {
		opts.Command = "clean"
	}
	if opts.DiscoverTimeout <= 0 {
		opts.DiscoverTimeout = owner.TimeoutScanPerRoot
	}
	plan := &Plan{RunID: journal.NewRunID(), Command: opts.Command}

	cats, scoped := discoverableCategories(opts)

	type discovered struct {
		cat     Category
		targets []Target
		err     error
	}
	results := make(map[string]discovered, len(cats))
	ch := make(chan discovered, len(cats))
	var wg sync.WaitGroup
	for _, cat := range cats {
		if !categoryIncluded(cat.ID(), opts.Exclude) {
			continue
		}
		wg.Add(1)
		go func(c Category) {
			defer wg.Done()
			dctx, cancel := context.WithTimeout(ctx, opts.DiscoverTimeout)
			defer cancel()
			ts, err := c.Discover(dctx, env)
			ch <- discovered{cat: c, targets: ts, err: err}
		}(cat)
	}
	wg.Wait()
	close(ch)
	for d := range ch {
		results[d.cat.ID()] = d
	}

	// Step 2/3/4/5 in category order for a stable plan.
	for _, cat := range cats {
		if !categoryIncluded(cat.ID(), opts.Exclude) {
			continue
		}
		d, ok := results[cat.ID()]
		if !ok {
			continue
		}
		if d.err != nil {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s: discovery failed: %v", cat.ID(), d.err))
			continue
		}
		targets := d.targets
		Measure(ctx, env, targets)
		for _, t := range targets {
			if !includeTarget(t, opts, env.Whitelist) {
				continue
			}
			if err := t.Validate(); err != nil {
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s: %v", t.ID, err))
				continue
			}
			selected := t.DefaultOn && t.AutoSelectable()
			if opts.NoOwner && t.Recovery == RecoveryOwner {
				selected = false
			}
			item := &PlannedItem{
				Target:        t,
				CategoryTitle: cat.Title(),
				CategoryNote:  cat.PreviewNote(),
				Selected:      selected,
				Status:        StatusPending,
			}
			if ap, ok := t.Action.(ArgvPlanner); ok {
				item.Argv = ap.PlannedArgv()
			}
			plan.Items = append(plan.Items, item)
		}
	}
	if len(opts.Only) > 0 && !scoped && len(plan.Items) == 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("no category matched --only %s", strings.Join(opts.Only, ", ")))
	}
	return plan, nil
}

// ApplyHooks runs the optional category hooks against plan: every Reconciler
// reconciles the plan, then every Warner's warnings are appended to
// plan.Warnings. Reconciliation always runs (it must be idempotent, and the
// CLI calls ApplyHooks again after the interactive selection so a superset item
// can supersede items the user picked); warnings are appended only once.
func ApplyHooks(cats []Category, plan *Plan) {
	if plan == nil {
		return
	}
	for _, c := range cats {
		if rec, ok := c.(Reconciler); ok {
			rec.Reconcile(plan)
		}
	}
	if plan.hooksApplied {
		return
	}
	plan.hooksApplied = true
	for _, c := range cats {
		if w, ok := c.(Warner); ok {
			plan.Warnings = append(plan.Warnings, w.Warnings()...)
		}
	}
}

// discoverableCategories narrows the category list to the ones --only can
// match. A selector's category is everything before its first dot, and a bare
// selector is itself a category id. Item-level narrowing stays in includeTarget.
//
// scoped is false when discovery had to cover every category: no selector
// prefix matched a category id, yet an item id does not always share its
// category's prefix (desktop-pkgs items are flatpak.*/snap.*), so skipping
// discovery could silently drop a real match.
func discoverableCategories(opts Options) (cats []Category, scoped bool) {
	if len(opts.Only) == 0 {
		return opts.Categories, true
	}
	known := make(map[string]bool, len(opts.Categories))
	for _, c := range opts.Categories {
		known[c.ID()] = true
	}
	prefixes := make(map[string]bool, len(opts.Only))
	unknown := false
	for _, s := range opts.Only {
		cat, _, _ := strings.Cut(s, ".")
		if cat == "" {
			continue
		}
		prefixes[cat] = true
		if !known[cat] {
			unknown = true
		}
	}
	if unknown || len(prefixes) == 0 {
		return opts.Categories, false
	}
	var matched []Category
	for _, c := range opts.Categories {
		if prefixes[c.ID()] {
			matched = append(matched, c)
		}
	}
	return matched, true
}

// categoryIncluded reports whether a category survives --exclude at the
// category level. --only is applied per item so item IDs work too.
func categoryIncluded(id string, exclude []string) bool {
	for _, e := range exclude {
		if e == id {
			return false
		}
	}
	return true
}

// includeTarget applies --only/--exclude, the whitelist, and --min-size.
func includeTarget(t Target, opts Options, wl *whitelist.Set) bool {
	if len(opts.Only) > 0 && !matchSelector(t, opts.Only) {
		return false
	}
	if len(opts.Exclude) > 0 && matchSelector(t, opts.Exclude) {
		return false
	}
	if t.Path != "" && wl != nil && wl.Contains(t.Path) {
		return false
	}
	if opts.MinSize > 0 && t.SizeKnown && t.SizeBytes >= 0 && t.SizeBytes < opts.MinSize {
		return false
	}
	return true
}

// matchSelector reports whether list selects the target, by category id, exact
// item id, or item-id prefix ("system" matches "system.pkg-cache").
func matchSelector(t Target, list []string) bool {
	if len(list) == 0 {
		return true
	}
	for _, s := range list {
		if s == "" {
			continue
		}
		if s == t.ID || s == t.Category {
			return true
		}
		if strings.HasPrefix(t.ID, s+".") {
			return true
		}
	}
	return false
}

// Summary is the outcome of Execute (steps 7-9).
type Summary struct {
	RunID       string
	Command     string
	Plan        *Plan
	OK          int
	Skipped     int
	Failed      int
	GuardStops  int
	Timeouts    int
	Freed       int64
	FreedKnown  bool
	DFBefore    uint64
	DFAfter     uint64
	JournalPath string
	Interrupted bool
}

// Executed returns the items that were attempted.
func (s *Summary) Executed() []*PlannedItem {
	var out []*PlannedItem
	for _, it := range s.Plan.Items {
		if it.Selected {
			out = append(out, it)
		}
	}
	return out
}

// Execute runs step 7 (execute), step 8 (journal), and gathers step 9 data.
// A tripped guard skips the item with a reason and never aborts the run. A
// cancelled context stops gracefully; already-completed items stay completed.
func Execute(ctx context.Context, env *Env, plan *Plan, jw *journal.Writer) *Summary {
	sum := &Summary{RunID: plan.RunID, Command: plan.Command, Plan: plan}
	if jw != nil {
		sum.JournalPath = jw.Path()
		_ = jw.Append(journal.Record{
			RunID:   plan.RunID,
			Command: plan.Command,
			Action:  journal.ActionRunStart,
			Status:  journal.StatusStart,
		})
	}
	sum.DFBefore = FreeBytes("/")

	for _, it := range plan.Items {
		if !it.Selected || it.Kept {
			continue
		}
		if ctx.Err() != nil {
			sum.Interrupted = true
			break
		}
		executeItem(ctx, env, it, sum)
		appendRecord(jw, plan, it)
	}
	sum.DFAfter = FreeBytes("/")
	if ctx.Err() != nil {
		sum.Interrupted = true
	}

	if jw != nil {
		_ = jw.Append(journal.Record{
			RunID:   plan.RunID,
			Command: plan.Command,
			Action:  journal.ActionRunEnd,
			Status:  journal.StatusEnd,
			Bytes:   sum.Freed,
		})
	}
	return sum
}

func executeItem(ctx context.Context, env *Env, it *PlannedItem, sum *Summary) {
	if it.Target.Guard != nil {
		if err := it.Target.Guard.Check(ctx, env); err != nil {
			it.Status = StatusSkipped
			it.Reason = err.Error()
			it.GuardStop = true
			sum.Skipped++
			sum.GuardStops++
			return
		}
	}
	res, err := it.Target.Action.Run(ctx, env.DryRun)
	if res.Argv != nil {
		it.Argv = res.Argv
	}
	switch {
	case env.DryRun:
		it.Status = StatusDryRun
		it.Freed = estimateFreed(it, res)
	case err == nil:
		it.Status = StatusOK
		it.Freed = estimateFreed(it, res)
		sum.OK++
		sum.Freed += it.Freed
		if it.Target.SizeKnown {
			sum.FreedKnown = true
		}
	default:
		if ctx.Err() != nil {
			it.Status = StatusSkipped
			it.Reason = "interrupted"
			sum.Skipped++
			return
		}
		var gs *safe.GuardStopError
		switch {
		case errors.As(err, &gs):
			it.Status = StatusSkipped
			it.Reason = gs.Reason
			it.GuardStop = true
			sum.Skipped++
			sum.GuardStops++
		case res.TimedOut:
			it.Status = StatusSkipped
			it.Reason = err.Error()
			it.TimedOut = true
			sum.Skipped++
			sum.Timeouts++
		default:
			it.Status = StatusFailed
			it.Reason = err.Error()
			sum.Failed++
		}
	}
}

// estimateFreed prefers the bytes an action reports; otherwise it uses the
// measured target size as the estimate.
func estimateFreed(it *PlannedItem, res Result) int64 {
	if res.Bytes > 0 {
		return res.Bytes
	}
	if it.Target.SizeKnown && it.Target.SizeBytes > 0 {
		return it.Target.SizeBytes
	}
	return 0
}

func appendRecord(jw *journal.Writer, plan *Plan, it *PlannedItem) {
	if jw == nil {
		return
	}
	action := journal.ActionOwner
	recovery := journal.RecoveryOwner
	if it.Target.Recovery == RecoveryTrash {
		action = journal.ActionTrash
		recovery = journal.RecoveryTrash
	}
	status := journal.StatusOK
	switch it.Status {
	case StatusSkipped:
		status = journal.StatusSkipped
	case StatusFailed:
		status = journal.StatusFailed
	case StatusDryRun:
		status = journal.StatusDryRun
	}
	_ = jw.Append(journal.Record{
		RunID:    plan.RunID,
		Command:  plan.Command,
		Item:     it.Target.ID,
		Action:   action,
		Recovery: recovery,
		Argv:     it.Argv,
		Path:     it.Target.Path,
		Status:   status,
		Bytes:    it.Freed,
		Reason:   it.Reason,
	})
}

// FreeBytes returns the free bytes on the filesystem containing path, or 0 if
// it cannot be determined.
func FreeBytes(path string) uint64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0
	}
	return st.Bavail * uint64(st.Bsize)
}
