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
	tea "github.com/charmbracelet/bubbletea"
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

func TestRenderPreviewShowsEmptyCategories(t *testing.T) {
	plan := samplePlan()
	plan.Scanned = []engine.CategoryRef{
		{ID: "system", Title: "System"},
		{ID: "containers", Title: "Containers"},
	}
	var buf bytes.Buffer
	RenderPreview(&buf, plan, "", false)
	out := buf.String()
	if !strings.Contains(out, "▸ Containers  nothing found") {
		t.Fatalf("empty category should render as nothing found:\n%s", out)
	}
	if strings.Contains(out, "▸ System  nothing found") {
		t.Fatalf("category with items must not be marked empty:\n%s", out)
	}
	if !strings.HasPrefix(strings.TrimSpace(strings.SplitN(out, "\n", 2)[0]), "Reclaimable now:") ||
		(!strings.HasPrefix(out, "Reclaimable now:") && !strings.Contains(out, "\nReclaimable now:")) {
		t.Fatalf("the reclaimable total should lead the preview:\n%s", out)
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

// projectsPlan builds a Projects category with two small groups (alpha, beta)
// plus an ungrouped system item. Projects groups start collapsed regardless of
// size: one row per project, expandable.
func projectsPlan() *engine.Plan {
	mk := func(id, group, title string) *engine.PlannedItem {
		return &engine.PlannedItem{
			Target: engine.Target{
				ID: id, Category: "projects", Display: "node_modules",
				Path: group + "/" + id, SizeBytes: 1024, SizeKnown: true,
				Recovery: engine.RecoveryTrash, Risk: engine.RiskLow,
				Group: group, GroupTitle: title,
			},
			CategoryTitle: "Projects",
		}
	}
	return &engine.Plan{
		RunID:   "TESTRUN",
		Command: "clean",
		Items: []*engine.PlannedItem{
			mk("projects.alpha-node_modules", "/w/alpha", "alpha"),
			mk("projects.alpha-node_modules-2", "/w/alpha", "alpha"),
			mk("projects.beta-node_modules", "/w/beta", "beta"),
			{
				Target: engine.Target{
					ID: "system.journald", Category: "system", Display: "systemd journal",
					SizeBytes: 1, SizeKnown: true, Recovery: engine.RecoveryOwner,
				},
				CategoryTitle: "System",
			},
		},
	}
}

func TestProjectsGroupsStartCollapsed(t *testing.T) {
	m := NewCleanModel(CleanConfig{Plan: projectsPlan()})
	var groups, items int
	for _, r := range m.rows {
		switch r.kind {
		case rowGroup:
			groups++
		case rowItem:
			if r.item.Target.Group != "" {
				items++
			}
		}
	}
	if groups != 2 {
		t.Fatalf("expected one row per project group, got %d group rows: %+v", groups, m.rows)
	}
	if items != 0 {
		t.Fatalf("project artifacts must be hidden behind their project row, got %d item rows", items)
	}
}

func TestProjectsGroupsExpandable(t *testing.T) {
	m := NewCleanModel(CleanConfig{Plan: projectsPlan()})
	for i, r := range m.rows {
		if r.kind == rowGroup && r.group.Title == "alpha" {
			m.cursor = i
			break
		}
	}
	m.expand()
	var alphaItems int
	for _, r := range m.rows {
		if r.kind == rowItem && r.group != nil && r.group.Title == "alpha" {
			alphaItems++
		}
	}
	if alphaItems != 2 {
		t.Fatalf("expanding alpha should reveal its 2 artifacts, got %d", alphaItems)
	}
	// beta stays collapsed.
	for _, r := range m.rows {
		if r.kind == rowItem && r.group != nil && r.group.Title == "beta" {
			t.Fatal("beta must stay collapsed")
		}
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

// twoCategoryPlan lists System before Developer caches so a name sort has to
// reorder the categories.
func twoCategoryPlan() *engine.Plan {
	mk := func(id, cat, title, display string, size int64) *engine.PlannedItem {
		return &engine.PlannedItem{
			Target: engine.Target{
				ID: id, Category: cat, Display: display,
				Path: "/tmp/" + id, SizeBytes: size, SizeKnown: true,
				Recovery: engine.RecoveryTrash, Risk: engine.RiskLow, DefaultOn: true,
			},
			CategoryTitle: title,
			Selected:      true,
		}
	}
	return &engine.Plan{RunID: "T", Command: "clean", Items: []*engine.PlannedItem{
		mk("system.a", "system", "System", "z-cache", 10),
		mk("dev.a", "dev", "Developer caches", "a-cache", 20),
	}}
}

// TestCleanHelpThenQuit pins the overlay behaviour: q closes the help, and the
// next q quits (and aborts the run).
func TestCleanHelpThenQuit(t *testing.T) {
	m := NewCleanModel(CleanConfig{Plan: samplePlan()})
	if cmd := pressKey(t, m, runeKey('?')); cmd != nil {
		t.Fatalf("opening help ran a command: %v", cmd)
	}
	if !m.help.ShowAll {
		t.Fatal("? should open the full help")
	}
	if cmd := pressKey(t, m, runeKey('q')); cmd != nil {
		t.Fatal("q while help is open must close the help, not quit")
	}
	if m.help.ShowAll || m.aborted {
		t.Fatalf("help still open=%v aborted=%v", m.help.ShowAll, m.aborted)
	}
	if cmd := pressKey(t, m, runeKey('q')); !isQuitCmd(cmd) {
		t.Fatal("the q after closing help must quit")
	}
	if !m.aborted {
		t.Fatal("quitting the clean TUI must abort the run")
	}
}

func TestCleanHelpThenEscThenQuit(t *testing.T) {
	m := NewCleanModel(CleanConfig{Plan: samplePlan()})
	pressKey(t, m, runeKey('?'))
	if cmd := pressKey(t, m, tea.KeyMsg{Type: tea.KeyEsc}); cmd != nil {
		t.Fatal("esc should close the help, not quit")
	}
	if m.help.ShowAll {
		t.Fatal("esc should close the help overlay")
	}
	if cmd := pressKey(t, m, runeKey('q')); !isQuitCmd(cmd) {
		t.Fatal("q after closing help must quit")
	}
}

// TestCleanSortNameKeepsSelectionAndCursor asserts n sorts by name without
// touching the selection or moving the cursor, and that N clears the selection.
func TestCleanSortNameKeepsSelectionAndCursor(t *testing.T) {
	plan := twoCategoryPlan()
	m := NewCleanModel(CleanConfig{Plan: plan})
	first, ok := m.currentRow()
	if !ok || first.kind != rowCategory || first.cat.Title != "System" {
		t.Fatalf("cursor should start on the System category, got %+v", m.rows)
	}

	pressKey(t, m, runeKey('n'))
	if m.sortMode != sortName {
		t.Fatal("n must sort by name")
	}
	for _, it := range plan.Items {
		if !it.Selected {
			t.Fatalf("n cleared the selection of %s", it.Target.ID)
		}
	}
	cur, ok := m.currentRow()
	if !ok || !sameRow(cur, first) {
		t.Fatalf("n moved the cursor off the System category: %+v", cur)
	}
	if got := m.rows[0].cat.Title; got != "Developer caches" {
		t.Fatalf("name sort should put Developer caches first, got %q", got)
	}

	pressKey(t, m, runeKey('N'))
	for _, it := range plan.Items {
		if it.Selected {
			t.Fatalf("N must clear the selection of %s", it.Target.ID)
		}
	}

	pressKey(t, m, runeKey('n'))
	if m.sortMode != sortDefault {
		t.Fatal("n should toggle back to the natural order")
	}
	if got := m.rows[0].cat.Title; got != "System" {
		t.Fatalf("natural order should put System first, got %q", got)
	}
}

// TestCleanSortSizeDescending asserts s orders every level of the tree by
// size, largest first, unknown sizes last, and pressing s again returns to the
// discovery order.
func TestCleanSortSizeDescending(t *testing.T) {
	mk := func(cat, title, group, groupTitle, display string, size int64, known bool) *engine.PlannedItem {
		return &engine.PlannedItem{
			Target: engine.Target{
				ID: display, Category: cat, Display: display,
				Path: "/tmp/" + display, SizeBytes: size, SizeKnown: known,
				Recovery: engine.RecoveryTrash, Risk: engine.RiskLow, DefaultOn: true,
				Group: group, GroupTitle: groupTitle,
			},
			CategoryTitle: title,
			Selected:      true,
		}
	}
	plan := &engine.Plan{RunID: "T", Command: "clean", Items: []*engine.PlannedItem{
		mk("dev", "Developer caches", "/p/a", "alpha", ".venv", 500, true),
		mk("dev", "Developer caches", "/p/a", "alpha", "__pycache__", 5, true),
		mk("dev", "Developer caches", "/p/b", "beta", "dist", 1000, true),
		mk("system", "System", "", "", "journald", 100, true),
		mk("system", "System", "", "", "mystery", 0, false),
	}}
	m := NewCleanModel(CleanConfig{Plan: plan})

	pressKey(t, m, runeKey('s'))
	if m.sortMode != sortSize {
		t.Fatal("s must sort by size")
	}
	var got []string
	for _, r := range m.rows {
		switch r.kind {
		case rowCategory:
			got = append(got, r.cat.Title)
		case rowGroup:
			got = append(got, r.group.Title)
		case rowItem:
			got = append(got, r.item.Target.Display)
		}
	}
	want := []string{
		"Developer caches", "beta", "dist", "alpha", ".venv", "__pycache__",
		"System", "journald", "mystery",
	}
	if len(got) != len(want) {
		t.Fatalf("size sort rows = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("size sort rows = %v, want %v", got, want)
		}
	}

	pressKey(t, m, runeKey('s'))
	if m.sortMode != sortDefault {
		t.Fatal("s should toggle back to the discovery order")
	}
	if got := m.rows[0].cat.Title; got != "Developer caches" {
		t.Fatalf("discovery order should lead with the plan's first category, got %q", got)
	}
}

// TestCleanFooterListsBindings asserts the footer names the keys it used to hide
// and points at the full help for the rest.
func TestCleanFooterListsBindings(t *testing.T) {
	m := NewCleanModel(CleanConfig{Plan: samplePlan()})
	m.width = 120
	m.help.Width = 120
	footer := m.footer()
	for _, want := range []string{
		"toggle", "expand", "collapse", "sort name", "sort size", "none", "all safe",
		"keep", "search", "review", "all keys", "quit",
	} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer hides %q:\n%s", want, footer)
		}
	}
	if lines := strings.Count(footer, "\n") + 1; lines > 2 {
		t.Fatalf("footer is %d lines, want at most 2:\n%s", lines, footer)
	}

	full := m.help.FullHelpView(m.keys.fullHelp())
	for _, want := range []string{"page up", "page down", "top", "bottom", "sort name", "sort size"} {
		if !strings.Contains(full, want) {
			t.Fatalf("full help hides %q:\n%s", want, full)
		}
	}
}
