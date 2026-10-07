package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
}

// Plan is the read-only result of pipeline steps 2-5 (discover, measure,
// filter, plan). Nothing here mutates the filesystem.
type Plan struct {
	RunID    string
	Command  string
	Items    []*PlannedItem
	Warnings []string
}

// Totals summarizes a set of items.
type Totals struct {
	Count   int   `json:"count"`
	Bytes   int64 `json:"bytes"`
	Unknown int   `json:"unknown"`
}

// CategoryGroup is a category's items for the preview.
type CategoryGroup struct {
	ID    string
	Title string
	Note  string
	Items []*PlannedItem
	Bytes int64
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
	// Permanent switches trash-contract items to a permanent delete.
	Permanent bool
}

// permanentSetter is implemented by trash actions that can be switched to a
// permanent delete.
type permanentSetter interface{ SetPermanent(bool) }

// Selected returns the items marked for execution.
func (p *Plan) Selected() []*PlannedItem {
	var out []*PlannedItem
	for _, it := range p.Items {
		if it.Selected {
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

// GroupedByCategory returns items grouped by category, preserving plan order.
func (p *Plan) GroupedByCategory() []CategoryGroup {
	index := map[string]int{}
	var groups []CategoryGroup
	for _, it := range p.Items {
		i, ok := index[it.Target.Category]
		if !ok {
			index[it.Target.Category] = len(groups)
			groups = append(groups, CategoryGroup{
				ID:    it.Target.Category,
				Title: it.CategoryTitle,
				Note:  it.CategoryNote,
			})
			i = len(groups) - 1
		}
		g := &groups[i]
		g.Items = append(g.Items, it)
		if it.Target.SizeKnown && it.Target.SizeBytes >= 0 {
			g.Bytes += it.Target.SizeBytes
		}
	}
	return groups
}

// Build runs steps 2-5: discover (parallel, per-category timeout), measure,
// filter, and plan. It never mutates the filesystem (invariant I1).
func Build(ctx context.Context, env *Env, opts Options) (*Plan, error) {
	if opts.Command == "" {
		opts.Command = "clean"
	}
	if opts.DiscoverTimeout <= 0 {
		opts.DiscoverTimeout = owner.TimeoutScanPerRoot
	}
	plan := &Plan{RunID: journal.NewRunID(), Command: opts.Command}

	type discovered struct {
		cat     Category
		targets []Target
		err     error
	}
	results := make(map[string]discovered, len(opts.Categories))
	ch := make(chan discovered, len(opts.Categories))
	var wg sync.WaitGroup
	for _, cat := range opts.Categories {
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
	for _, cat := range opts.Categories {
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
			if opts.Permanent {
				if ps, ok := t.Action.(permanentSetter); ok {
					ps.SetPermanent(true)
				}
			}
			selected := t.DefaultOn && t.Risk != RiskHigh
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
	return plan, nil
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
		if !it.Selected {
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
	if it.Target.Recovery == RecoveryTrash {
		action = journal.ActionTrash
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
		RunID:   plan.RunID,
		Command: plan.Command,
		Item:    it.Target.ID,
		Action:  action,
		Argv:    it.Argv,
		Path:    it.Target.Path,
		Status:  status,
		Bytes:   it.Freed,
		Reason:  it.Reason,
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

// SortItemsBySize orders items largest-first, unknown sizes last. It is used by
// the preview so the biggest wins are visible first.
func SortItemsBySize(items []*PlannedItem) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].Target, items[j].Target
		if a.SizeKnown != b.SizeKnown {
			return a.SizeKnown
		}
		return a.SizeBytes > b.SizeBytes
	})
}
