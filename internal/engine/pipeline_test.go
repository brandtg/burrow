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
	act := action.NewTrash(victim, tr, snap, nil, false)
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

func TestExecutePermanentDeletesAndJournals(t *testing.T) {
	env := testEnv(t, false)
	jw, err := journal.NewWriter(env.Paths.Journal)
	if err != nil {
		t.Fatal(err)
	}
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
	act := action.NewTrash(victim, tr, snap, nil, false)
	tgt := engine.Target{
		ID: "fake.trash", Category: "fake", Display: "victim", Path: victim,
		Action: act, Recovery: engine.RecoveryTrash, Risk: engine.RiskLow, DefaultOn: true,
	}
	cat := fakeCategory{id: "fake", targets: []engine.Target{tgt}}
	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{cat},
		Permanent:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Items[0].Permanent {
		t.Fatalf("--permanent must mark the item permanent: %+v", plan.Items[0])
	}
	sum := engine.Execute(context.Background(), env, plan, jw)
	if sum.OK != 1 {
		t.Fatalf("expected the permanent delete to succeed, got %+v", sum)
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatalf("permanent delete must remove the path, stat err = %v", err)
	}

	records, err := journal.Read(env.Paths.Journal)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range records {
		if r.Item != "fake.trash" {
			continue
		}
		found = true
		if r.Action != journal.ActionDelete {
			t.Fatalf("permanent delete must journal action=%q, got %q", journal.ActionDelete, r.Action)
		}
		if r.Recovery != journal.RecoveryDelete {
			t.Fatalf("permanent delete must journal recovery=%q, got %q", journal.RecoveryDelete, r.Recovery)
		}
	}
	if !found {
		t.Fatalf("no journal record for the permanent item: %+v", records)
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
