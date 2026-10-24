package tui

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/whitelist"
)

func samplePlan() *engine.Plan {
	mk := func(id, display string, size int64, rec engine.Recovery, root bool, risk engine.Risk, on bool, argv []string) *engine.PlannedItem {
		return &engine.PlannedItem{
			Target: engine.Target{
				ID: id, Category: "system", Display: display,
				SizeBytes: size, SizeKnown: true, Recovery: rec,
				NeedsRoot: root, Risk: risk, DefaultOn: on,
			},
			CategoryTitle: "System",
			Selected:      on && risk != engine.RiskHigh,
			Argv:          argv,
		}
	}
	return &engine.Plan{
		RunID:   "TESTRUN",
		Command: "clean",
		Items: []*engine.PlannedItem{
			mk("system.pkg-cache", "package cache", 1288490188, engine.RecoveryOwner, true, engine.RiskLow, true, []string{"sudo", "--", "apt-get", "clean"}),
			mk("system.journald", "systemd journal", 536870912, engine.RecoveryOwner, true, engine.RiskLow, true, []string{"sudo", "--", "journalctl", "--vacuum-size=500M"}),
			mk("system.orphans", "orphaned packages", 0, engine.RecoveryOwner, true, engine.RiskHigh, false, []string{"sudo", "--", "apt-get", "autoremove", "--purge"}),
		},
	}
}

func TestRenderPreviewGolden(t *testing.T) {
	var buf bytes.Buffer
	RenderPreview(&buf, samplePlan(), "Ubuntu 24.04 \u00b7 apt \u00b7 118.0 GiB free", false)
	got := buf.String()

	golden := filepath.Join("..", "..", "testdata", "clean", "preview.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (run with UPDATE_GOLDEN=1 to create)", err)
	}
	if got != string(want) {
		t.Fatalf("preview golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderReviewListsExactArgv(t *testing.T) {
	var buf bytes.Buffer
	RenderReview(&buf, samplePlan())
	got := buf.String()
	if !bytes.Contains([]byte(got), []byte("sudo -- apt-get clean")) {
		t.Fatalf("review missing exact argv:\n%s", got)
	}
	if bytes.Contains([]byte(got), []byte("autoremove")) {
		t.Fatalf("review must not list an unselected high-risk item:\n%s", got)
	}
}

func bigPlan() *engine.Plan {
	var items []*engine.PlannedItem
	for i := 0; i < 30; i++ {
		items = append(items, &engine.PlannedItem{
			Target: engine.Target{
				ID:        fmt.Sprintf("dev.pycache.%d", i),
				Category:  "dev",
				Display:   "Python bytecode cache",
				Path:      fmt.Sprintf("/home/u/Projects/p%d/__pycache__", i),
				SizeBytes: 1024, SizeKnown: true,
				Recovery: engine.RecoveryTrash, Risk: engine.RiskLow, DefaultOn: true,
				Group:      fmt.Sprintf("/home/u/Projects/p%d", i),
				GroupTitle: fmt.Sprintf("p%d", i),
			},
			CategoryTitle: "Developer caches",
			Selected:      true,
		})
	}
	return &engine.Plan{RunID: "TESTRUN", Command: "clean", Items: items}
}

func TestCleanModelCollapsesLargeCategories(t *testing.T) {
	m := NewCleanModel(CleanConfig{Plan: bigPlan()})
	if len(m.rows) != 1 || m.rows[0].kind != rowCategory {
		t.Fatalf("large category should start collapsed: %d rows", len(m.rows))
	}
	m.expand()
	if len(m.rows) <= 1 {
		t.Fatalf("expanding should reveal rows, got %d", len(m.rows))
	}
}

func TestCleanModelCategoryToggle(t *testing.T) {
	plan := bigPlan()
	m := NewCleanModel(CleanConfig{Plan: plan})
	m.toggle()
	for _, it := range plan.Items {
		if it.Selected {
			t.Fatal("category toggle should deselect all when all are selected")
		}
	}
	m.toggle()
	for _, it := range plan.Items {
		if !it.Selected {
			t.Fatal("category toggle should select all non-high-risk members")
		}
	}
}

func TestCleanModelFilter(t *testing.T) {
	m := NewCleanModel(CleanConfig{Plan: bigPlan()})
	m.filter.SetValue("p7")
	m.rebuildRows()
	var items []*engine.PlannedItem
	for _, r := range m.rows {
		if r.kind == rowItem {
			items = append(items, r.item)
		}
	}
	if len(items) != 1 || !strings.Contains(items[0].Target.Path, "/p7/") {
		t.Fatalf("filter p7 = %d item(s)", len(items))
	}
}

func TestReviewTextDryRun(t *testing.T) {
	if got := reviewText(samplePlan(), true); !strings.Contains(got, "dry run: nothing will run") {
		t.Fatalf("dry-run review must say nothing will run:\n%s", got)
	}
}

func TestReviewAggregatesLargeTrashSelection(t *testing.T) {
	got := reviewText(bigPlan(), false)
	if strings.Contains(got, "/home/u/Projects/p0/__pycache__") {
		t.Fatalf("large selection must aggregate, not list every path:\n%s", got)
	}
	if !strings.Contains(got, "trash 30 item(s)") {
		t.Fatalf("expected an aggregated trash line:\n%s", got)
	}
}

func TestCleanModelViewRenders(t *testing.T) {
	m := NewCleanModel(CleanConfig{Plan: bigPlan(), Platform: "Fedora · dnf", DryRun: true})
	view := m.View()
	if !strings.Contains(view, "Developer caches") {
		t.Fatalf("preview view missing category:\n%s", view)
	}
	m.screen = 1
	if got := m.View(); !strings.Contains(got, "dry run: nothing will run") {
		t.Fatalf("review view missing dry-run notice:\n%s", got)
	}
}

func TestCleanModelKeepWhitelists(t *testing.T) {
	wlPath := filepath.Join(t.TempDir(), "whitelist")
	mgr := whitelist.NewManager(wlPath)
	if err := mgr.Load(); err != nil {
		t.Fatal(err)
	}
	item := &engine.PlannedItem{
		Target: engine.Target{
			ID: "user-cache.app-caches.huggingface", Category: "user-cache",
			Display: "huggingface", Path: "/home/u/.cache/huggingface",
			SizeBytes: 10, SizeKnown: true, Recovery: engine.RecoveryTrash,
			Risk: engine.RiskMed, Regen: engine.RegenExpensive,
		},
		CategoryTitle: "User cache",
	}
	plan := &engine.Plan{RunID: "T", Command: "clean", Items: []*engine.PlannedItem{item}}
	m := NewCleanModel(CleanConfig{Plan: plan, Whitelist: mgr})
	for i, r := range m.rows {
		if r.kind == rowItem {
			m.cursor = i
		}
	}
	m.keep()
	if !item.Kept || item.Selected {
		t.Fatalf("keep must mark kept and deselect: %+v", item)
	}
	data, err := os.ReadFile(wlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "/home/u/.cache/huggingface") {
		t.Fatalf("whitelist not written: %q", data)
	}
}

func TestSelectAllSkipsExpensiveAndGated(t *testing.T) {
	mk := func(id string, regen engine.Regen, gated bool) *engine.PlannedItem {
		return &engine.PlannedItem{Target: engine.Target{
			ID: id, Category: "dev", Display: id, Recovery: engine.RecoveryTrash,
			Risk: engine.RiskLow, Regen: regen, Gated: gated,
		}, CategoryTitle: "Dev"}
	}
	free := mk("dev.free", engine.RegenFree, false)
	expensive := mk("dev.expensive", engine.RegenExpensive, false)
	gated := mk("dev.gated", engine.RegenCheap, true)
	plan := &engine.Plan{RunID: "T", Command: "clean", Items: []*engine.PlannedItem{free, expensive, gated}}
	m := NewCleanModel(CleanConfig{Plan: plan})
	m.selectAll()
	if !free.Selected {
		t.Fatal("free item should be selected by select-all")
	}
	if expensive.Selected {
		t.Fatal("expensive item must not be selected by select-all")
	}
	if gated.Selected {
		t.Fatal("gated item must not be selected by select-all")
	}
}
