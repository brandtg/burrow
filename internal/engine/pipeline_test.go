package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brandtg/burrow/internal/action"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/distro"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
	"github.com/brandtg/burrow/internal/whitelist"
)

func testEnv(t *testing.T, dryRun bool) *engine.Env {
	t.Helper()
	dir := t.TempDir()
	return &engine.Env{
		Home:     filepath.Join(dir, "home"),
		XDGCache: filepath.Join(dir, "cache"),
		XDGData:  filepath.Join(dir, "data"),
		XDGState: filepath.Join(dir, "state"),
		Run:      owner.New(dryRun),
		Cfg:      config.Default(),
		Adapter:  distro.For(distro.Info{ID: "ubuntu"}),
		Paths: config.Paths{
			Journal:  filepath.Join(dir, "operations.jsonl"),
			TrashDir: filepath.Join(dir, "Trash"),
		},
		Whitelist: &whitelist.Set{},
		DryRun:    dryRun,
	}
}

type fakeAction struct {
	recovery engine.Recovery
	run      func(context.Context, bool) (engine.Result, error)
}

func (a fakeAction) Describe() string { return "fake" }
func (a fakeAction) Recovery() engine.Recovery {
	if a.recovery == 0 {
		return engine.RecoveryOwner
	}
	return a.recovery
}
func (a fakeAction) Run(ctx context.Context, dryRun bool) (engine.Result, error) {
	if a.run != nil {
		return a.run(ctx, dryRun)
	}
	return engine.Result{Status: engine.StatusOK}, nil
}

type fakeGuard struct{ err error }

func (g fakeGuard) Check(context.Context, *engine.Env) error { return g.err }

type fakeCategory struct {
	id      string
	targets []engine.Target
}

func (c fakeCategory) ID() string          { return c.id }
func (c fakeCategory) Title() string       { return strings.ToUpper(c.id) }
func (c fakeCategory) PreviewNote() string { return "note-" + c.id }
func (c fakeCategory) Discover(context.Context, *engine.Env) ([]engine.Target, error) {
	return c.targets, nil
}

func target(id string, risk engine.Risk, on bool, path string) engine.Target {
	return engine.Target{
		ID:        id,
		Category:  "fake",
		Display:   id,
		Path:      path,
		SizeBytes: 100,
		SizeKnown: true,
		Action:    fakeAction{recovery: engine.RecoveryOwner},
		Recovery:  engine.RecoveryOwner,
		Risk:      risk,
		DefaultOn: on,
	}
}

func TestBuildFilterSelectAndTotals(t *testing.T) {
	env := testEnv(t, true)
	wl, err := whitelist.Parse(strings.NewReader("/skip/me\n"))
	if err != nil {
		t.Fatal(err)
	}
	env.Whitelist = wl
	cat := fakeCategory{id: "fake", targets: []engine.Target{
		target("fake.low", engine.RiskLow, true, "/keep/low"),
		target("fake.high", engine.RiskHigh, true, "/keep/high"),
		target("fake.med", engine.RiskMed, false, "/keep/med"),
		target("fake.skip", engine.RiskLow, true, "/skip/me"),
	}}
	plan, err := engine.Build(context.Background(), env, engine.Options{Categories: []engine.Category{cat}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 3 {
		t.Fatalf("whitelist should drop one item, got %d (warnings: %v)", len(plan.Items), plan.Warnings)
	}
	sel := plan.Selected()
	if len(sel) != 1 || sel[0].Target.ID != "fake.low" {
		t.Fatalf("expected only fake.low selected, got %v", ids(sel))
	}
	if plan.Items[1].Selected {
		t.Fatal("high-risk item must never be auto-selected")
	}
	if tot := plan.SelectedTotals(); tot.Count != 1 || tot.Bytes != 100 {
		t.Fatalf("unexpected totals %+v", tot)
	}
}

func TestBuildNoOwner(t *testing.T) {
	env := testEnv(t, true)
	trashT := target("fake.trash", engine.RiskLow, true, "/keep/trash")
	trashT.Recovery = engine.RecoveryTrash
	trashT.Action = fakeAction{recovery: engine.RecoveryTrash}
	cat := fakeCategory{id: "fake", targets: []engine.Target{
		target("fake.owner", engine.RiskLow, true, ""),
		trashT,
	}}
	plan, err := engine.Build(context.Background(), env, engine.Options{Categories: []engine.Category{cat}, NoOwner: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range plan.Items {
		if it.Target.Recovery == engine.RecoveryOwner && it.Selected {
			t.Fatalf("owner item %s should not be selected under --no-owner", it.Target.ID)
		}
		if it.Target.Recovery == engine.RecoveryTrash && !it.Selected {
			t.Fatalf("trash item %s should be selected", it.Target.ID)
		}
	}
}

func TestExecuteDryRunDoesNotMutate(t *testing.T) {
	env := testEnv(t, true)
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := safe.TakeSnapshot(victim)
	if err != nil {
		t.Fatal(err)
	}
	tr := trash.New(env.Paths.TrashDir, os.Getuid())
	act := action.NewTrash(victim, tr, snap, nil)
	tgt := engine.Target{
		ID: "fake.trash", Category: "fake", Display: "victim", Path: victim,
		Action: act, Recovery: engine.RecoveryTrash, Risk: engine.RiskLow, DefaultOn: true,
	}
	cat := fakeCategory{id: "fake", targets: []engine.Target{tgt}}
	plan, err := engine.Build(context.Background(), env, engine.Options{Categories: []engine.Category{cat}})
	if err != nil {
		t.Fatal(err)
	}
	sum := engine.Execute(context.Background(), env, plan, nil)
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("dry-run mutated the filesystem: %v", err)
	}
	if sum.OK != 0 || plan.Items[0].Status != engine.StatusDryRun {
		t.Fatalf("expected dry-run status, got %+v", plan.Items[0])
	}
}

func TestExecuteGuardStopSkipsAndJournals(t *testing.T) {
	env := testEnv(t, false)
	jw, err := journal.NewWriter(env.Paths.Journal)
	if err != nil {
		t.Fatal(err)
	}
	tgt := target("fake.guarded", engine.RiskLow, true, "")
	tgt.Guard = fakeGuard{err: safe.GuardStop("owner busy")}
	cat := fakeCategory{id: "fake", targets: []engine.Target{tgt}}
	plan, err := engine.Build(context.Background(), env, engine.Options{Categories: []engine.Category{cat}})
	if err != nil {
		t.Fatal(err)
	}
	sum := engine.Execute(context.Background(), env, plan, jw)
	if sum.Skipped != 1 || sum.GuardStops != 1 || sum.OK != 0 {
		t.Fatalf("unexpected summary %+v", sum)
	}
	if !plan.Items[0].GuardStop || plan.Items[0].Reason == "" {
		t.Fatalf("guard stop not recorded: %+v", plan.Items[0])
	}

	records, err := journal.Read(env.Paths.Journal)
	if err != nil {
		t.Fatal(err)
	}
	var starts, ends, items int
	for _, r := range records {
		switch r.Action {
		case journal.ActionRunStart:
			starts++
		case journal.ActionRunEnd:
			ends++
		default:
			items++
			if r.Status != journal.StatusSkipped || r.Reason == "" {
				t.Fatalf("unexpected item record %+v", r)
			}
		}
	}
	if starts != 1 || ends != 1 || items != 1 {
		t.Fatalf("journal framing wrong: starts=%d ends=%d items=%d", starts, ends, items)
	}
}

func TestExecuteFailureIsPartial(t *testing.T) {
	env := testEnv(t, false)
	tgt := target("fake.fail", engine.RiskLow, true, "")
	tgt.Action = fakeAction{recovery: engine.RecoveryOwner, run: func(context.Context, bool) (engine.Result, error) {
		return engine.Result{Status: engine.StatusFailed}, errors.New("boom")
	}}
	cat := fakeCategory{id: "fake", targets: []engine.Target{tgt}}
	plan, err := engine.Build(context.Background(), env, engine.Options{Categories: []engine.Category{cat}})
	if err != nil {
		t.Fatal(err)
	}
	sum := engine.Execute(context.Background(), env, plan, nil)
	if sum.Failed != 1 {
		t.Fatalf("expected 1 failure, got %+v", sum)
	}
}

func TestSelectorMatchesCategoryAndPrefix(t *testing.T) {
	env := testEnv(t, true)
	cat := fakeCategory{id: "system", targets: []engine.Target{
		target("system.pkg-cache", engine.RiskLow, true, ""),
		target("system.journald", engine.RiskLow, true, ""),
	}}
	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{cat},
		Only:       []string{"system.journald"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || plan.Items[0].Target.ID != "system.journald" {
		t.Fatalf("selector failed: %v", ids(plan.Items))
	}
}

func ids(items []*engine.PlannedItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Target.ID)
	}
	return out
}

// probeCategory records whether its Discover ran, so tests can prove that
// --only scopes discovery instead of filtering after the fact.
type probeCategory struct {
	id         string
	discovered *bool
	targets    []engine.Target
}

func (c probeCategory) ID() string          { return c.id }
func (c probeCategory) Title() string       { return strings.ToUpper(c.id) }
func (c probeCategory) PreviewNote() string { return "note-" + c.id }
func (c probeCategory) Discover(context.Context, *engine.Env) ([]engine.Target, error) {
	*c.discovered = true
	return c.targets, nil
}

func catTarget(cat, item string) engine.Target {
	t := target(cat+"."+item, engine.RiskLow, true, "")
	t.Category = cat
	return t
}

func TestBuildOnlyScopesDiscovery(t *testing.T) {
	env := testEnv(t, true)
	devRan, sysRan := false, false
	dev := probeCategory{id: "dev", discovered: &devRan, targets: []engine.Target{catTarget("dev", "npm")}}
	sys := probeCategory{id: "system", discovered: &sysRan, targets: []engine.Target{catTarget("system", "pkg-cache")}}

	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{dev, sys},
		Only:       []string{"dev"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !devRan {
		t.Fatal("dev.Discover did not run under --only dev")
	}
	if sysRan {
		t.Fatal("system.Discover ran under --only dev; --only must scope discovery")
	}
	if got := ids(plan.Items); len(got) != 1 || got[0] != "dev.npm" {
		t.Fatalf("items = %v, want [dev.npm]", got)
	}
}

func TestBuildOnlyItemSelectorStillNarrowsItems(t *testing.T) {
	env := testEnv(t, true)
	devRan, sysRan := false, false
	dev := probeCategory{id: "dev", discovered: &devRan, targets: []engine.Target{catTarget("dev", "npm")}}
	sys := probeCategory{id: "system", discovered: &sysRan, targets: []engine.Target{
		catTarget("system", "pkg-cache"),
		catTarget("system", "journald"),
	}}

	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{dev, sys},
		Only:       []string{"system.journald"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if devRan {
		t.Fatal("dev.Discover ran under --only system.journald")
	}
	if !sysRan {
		t.Fatal("system.Discover did not run under --only system.journald")
	}
	if got := ids(plan.Items); len(got) != 1 || got[0] != "system.journald" {
		t.Fatalf("items = %v, want [system.journald]", got)
	}
}

func TestBuildOnlyNoCategoryMatchWarns(t *testing.T) {
	env := testEnv(t, true)
	dev := probeCategory{id: "dev", discovered: new(bool), targets: []engine.Target{catTarget("dev", "npm")}}

	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{dev},
		Only:       []string{"nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 0 {
		t.Fatalf("items = %v, want none", ids(plan.Items))
	}
	if len(plan.Warnings) != 1 || !strings.Contains(plan.Warnings[0], "no category matched --only nope") {
		t.Fatalf("warnings = %v, want the no-category-match note", plan.Warnings)
	}
}

func TestBuildOnlyForeignItemPrefixStillMatches(t *testing.T) {
	// desktop-pkgs items are flatpak.*/snap.*, so a selector prefix that is not
	// a category id must not skip discovery nor drop the item.
	env := testEnv(t, true)
	devRan, deskRan := false, false
	dev := probeCategory{id: "dev", discovered: &devRan, targets: []engine.Target{catTarget("dev", "npm")}}
	desk := probeCategory{id: "desktop-pkgs", discovered: &deskRan, targets: []engine.Target{
		catTarget("flatpak", "unused"),
	}}
	desk.targets[0].Category = "desktop-pkgs"

	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{dev, desk},
		Only:       []string{"flatpak.unused"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !deskRan {
		t.Fatal("desktop-pkgs.Discover did not run for --only flatpak.unused")
	}
	if !devRan {
		t.Fatal("an unknown selector prefix must not skip discovery of other categories")
	}
	if got := ids(plan.Items); len(got) != 1 || got[0] != "flatpak.unused" {
		t.Fatalf("items = %v, want [flatpak.unused]", got)
	}
	if len(plan.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", plan.Warnings)
	}
}

// hookCategory implements Reconciler and Warner and records reconcile calls.
type hookCategory struct {
	warns      []string
	reconciles int
}

func (c *hookCategory) ID() string          { return "hook" }
func (c *hookCategory) Title() string       { return "Hook" }
func (c *hookCategory) PreviewNote() string { return "" }
func (c *hookCategory) Discover(context.Context, *engine.Env) ([]engine.Target, error) {
	return nil, nil
}
func (c *hookCategory) Reconcile(plan *engine.Plan) {
	c.reconciles++
	for _, it := range plan.Items {
		it.Selected = false
	}
}
func (c *hookCategory) Warnings() []string { return c.warns }

func TestApplyHooksReconcilesEveryCallAndMergesWarningsOnce(t *testing.T) {
	plan := &engine.Plan{Items: []*engine.PlannedItem{
		{Target: engine.Target{ID: "hook.x"}, Selected: true},
	}}
	cat := &hookCategory{warns: []string{"root missing"}}
	engine.ApplyHooks([]engine.Category{cat}, plan)
	engine.ApplyHooks([]engine.Category{cat}, plan)
	// Reconcile must run on every call: the CLI calls ApplyHooks again after
	// the interactive selection so a superset item can supersede picked items.
	if cat.reconciles != 2 {
		t.Fatalf("Reconcile ran %d times, want 2", cat.reconciles)
	}
	if len(plan.Warnings) != 1 || plan.Warnings[0] != "root missing" {
		t.Fatalf("warnings = %v, want one merged note", plan.Warnings)
	}
	if plan.Items[0].Selected {
		t.Fatal("Reconcile did not run")
	}
}

func TestRecoveryDeleteContract(t *testing.T) {
	if got := engine.RecoveryDelete.String(); got != "delete" {
		t.Fatalf("RecoveryDelete.String() = %q, want delete", got)
	}
	if !engine.RecoveryDelete.Valid() {
		t.Fatal("RecoveryDelete must be a valid contract")
	}
	tgt := engine.Target{
		ID:       "trash.empty",
		Recovery: engine.RecoveryDelete,
		Action:   fakeAction{recovery: engine.RecoveryDelete},
	}
	if err := tgt.Validate(); err != nil {
		t.Fatalf("delete-contract target without a path must validate: %v", err)
	}
}

func TestRegenString(t *testing.T) {
	cases := map[engine.Regen]string{
		engine.RegenFree:      "free",
		engine.RegenCheap:     "cheap",
		engine.RegenExpensive: "expensive",
		engine.RegenData:      "data",
	}
	for r, want := range cases {
		if got := r.String(); got != want {
			t.Fatalf("Regen(%d).String() = %q, want %q", r, got, want)
		}
	}
}

func TestAutoSelectable(t *testing.T) {
	cases := []struct {
		name string
		tgt  engine.Target
		want bool
	}{
		{"free-low", engine.Target{Risk: engine.RiskLow, Regen: engine.RegenFree}, true},
		{"cheap-low", engine.Target{Risk: engine.RiskLow, Regen: engine.RegenCheap}, true},
		{"expensive-low", engine.Target{Risk: engine.RiskLow, Regen: engine.RegenExpensive}, false},
		{"data-low", engine.Target{Risk: engine.RiskLow, Regen: engine.RegenData}, false},
		{"high-free", engine.Target{Risk: engine.RiskHigh, Regen: engine.RegenFree}, false},
		{"gated-cheap", engine.Target{Risk: engine.RiskLow, Regen: engine.RegenCheap, Gated: true}, false},
	}
	for _, c := range cases {
		if got := c.tgt.AutoSelectable(); got != c.want {
			t.Fatalf("%s: AutoSelectable() = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBuildNeverPreselectsExpensiveOrGated(t *testing.T) {
	env := testEnv(t, true)
	free := target("fake.free", engine.RiskLow, true, "")
	expensive := target("fake.expensive", engine.RiskLow, true, "")
	expensive.Regen = engine.RegenExpensive
	gated := target("fake.gated", engine.RiskLow, true, "")
	gated.Gated = true
	cat := fakeCategory{id: "fake", targets: []engine.Target{free, expensive, gated}}
	plan, err := engine.Build(context.Background(), env, engine.Options{Categories: []engine.Category{cat}})
	if err != nil {
		t.Fatal(err)
	}
	sel := map[string]bool{}
	for _, it := range plan.Selected() {
		sel[it.Target.ID] = true
	}
	if !sel["fake.free"] {
		t.Fatalf("free item must be pre-selected; got %v", ids(plan.Selected()))
	}
	if sel["fake.expensive"] {
		t.Fatal("expensive item must not be pre-selected")
	}
	if sel["fake.gated"] {
		t.Fatal("gated item must not be pre-selected")
	}
}

func TestKeptItemNeverExecutes(t *testing.T) {
	env := testEnv(t, false)
	tgt := target("fake.kept", engine.RiskLow, true, "")
	ran := false
	tgt.Action = fakeAction{recovery: engine.RecoveryOwner, run: func(context.Context, bool) (engine.Result, error) {
		ran = true
		return engine.Result{Status: engine.StatusOK}, nil
	}}
	cat := fakeCategory{id: "fake", targets: []engine.Target{tgt}}
	plan, err := engine.Build(context.Background(), env, engine.Options{Categories: []engine.Category{cat}})
	if err != nil {
		t.Fatal(err)
	}
	plan.Items[0].Kept = true
	sum := engine.Execute(context.Background(), env, plan, nil)
	if ran || sum.OK != 0 {
		t.Fatalf("kept item must not execute: ran=%v sum=%+v", ran, sum)
	}
}

func TestGroupedTreeBucketsByGroup(t *testing.T) {
	plan := &engine.Plan{Items: []*engine.PlannedItem{
		{Target: engine.Target{ID: "a", Category: "dev", Group: "p1", GroupTitle: "proj1"}, CategoryTitle: "Dev"},
		{Target: engine.Target{ID: "b", Category: "dev", Group: "p1", GroupTitle: "proj1"}, CategoryTitle: "Dev"},
		{Target: engine.Target{ID: "c", Category: "dev", Group: "p2", GroupTitle: "proj2"}, CategoryTitle: "Dev"},
		{Target: engine.Target{ID: "d", Category: "system"}, CategoryTitle: "System"},
	}}
	cats := plan.GroupedTree()
	if len(cats) != 2 {
		t.Fatalf("categories = %d, want 2", len(cats))
	}
	dev := cats[0]
	if dev.ID != "dev" || len(dev.Items) != 3 {
		t.Fatalf("dev category = %+v", dev)
	}
	if len(dev.Groups) != 2 {
		t.Fatalf("dev groups = %d, want 2", len(dev.Groups))
	}
	if dev.Groups[0].Title != "proj1" || len(dev.Groups[0].Items) != 2 {
		t.Fatalf("group 0 = %+v", dev.Groups[0])
	}
	if dev.Groups[1].Title != "proj2" || len(dev.Groups[1].Items) != 1 {
		t.Fatalf("group 1 = %+v", dev.Groups[1])
	}
	if got := cats[1].Groups[0].ID; got != "" {
		t.Fatalf("ungrouped category implicit group id = %q, want empty", got)
	}
}
