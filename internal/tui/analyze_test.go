package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brandtg/burrow/internal/scan"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestParentCursorFollowsSort(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{
			{Name: "zulu", Path: "/root/zulu", IsDir: true, Size: 100, SizeKnown: true,
				Children: []*scan.Node{{Name: "z1", Path: "/root/zulu/z1", Size: 1, SizeKnown: true}}},
			{Name: "alpha", Path: "/root/alpha", IsDir: true, Size: 10, SizeKnown: true,
				Children: []*scan.Node{{Name: "a1", Path: "/root/alpha/a1", Size: 1, SizeKnown: true}}},
		},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
	// Name sort flips the order relative to the stored (size-sorted) children,
	// so indexing p.Children would put the cursor on the wrong row.
	m.setSort(SortByName)
	m.move(1)
	if got := m.entries[m.cursor].Name; got != "zulu" {
		t.Fatalf("cursor on %s, want zulu", got)
	}
	m.enter()
	if m.dir.Name != "zulu" {
		t.Fatalf("entered %s, want zulu", m.dir.Name)
	}
	m.parent()
	if m.dir != root {
		t.Fatalf("parent dir = %s, want root", m.dir.Name)
	}
	if m.entries[m.cursor] != root.Children[0] {
		t.Fatalf("cursor on %s, want the exited directory zulu", m.entries[m.cursor].Name)
	}
}

func TestAnalyzeBackgroundLoadShowsProgress(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{{Name: "a", Path: "/root/a", Size: 10, SizeKnown: true}},
	}
	m := NewAnalyzeModel(AnalyzeConfig{
		Load:  func(context.Context, func(scan.Progress)) (*scan.Node, error) { return root, nil },
		Color: false,
	})
	if m.root != nil {
		t.Fatal("model should start without a tree when Load is set")
	}
	if !strings.Contains(m.loadingView(), "scanning") {
		t.Fatalf("loading view missing scanner: %q", m.loadingView())
	}
	m.loading = true
	m.Update(progressMsg(scan.Progress{ItemCount: 5, TotalUsage: 2048, CurrentDir: "/root/sub"}))
	if view := m.loadingView(); !strings.Contains(view, "5 items") || !strings.Contains(view, "/root/sub") {
		t.Fatalf("progress not rendered: %q", view)
	}
	m.Update(loadDoneMsg{root: root})
	if m.loading {
		t.Fatal("loading should stop once the scan finishes")
	}
	if m.root != root || len(m.entries) != 1 {
		t.Fatalf("tree not installed: root=%v entries=%d", m.root, len(m.entries))
	}
}

func TestAnalyzeBackgroundLoadError(t *testing.T) {
	m := NewAnalyzeModel(AnalyzeConfig{
		Load:  func(context.Context, func(scan.Progress)) (*scan.Node, error) { return nil, nil },
		Color: false,
	})
	m.loading = true
	m.Update(loadDoneMsg{err: errors.New("boom")})
	if m.loadErr == nil {
		t.Fatal("expected the load error to be recorded")
	}
	if view := m.loadingView(); !strings.Contains(view, "boom") {
		t.Fatalf("error not rendered: %q", view)
	}
}

func TestAnalyzeHeaderWarnsOnPartialScan(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{
			{Name: "locked", Path: "/root/locked", IsDir: true, SizeKnown: false},
		},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
	if m.unknown != 1 {
		t.Fatalf("unknown = %d, want 1", m.unknown)
	}
	if got := m.header(); !strings.Contains(got, "partial scan") {
		t.Fatalf("header missing partial-scan banner: %q", got)
	}

	complete := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{{Name: "a", Path: "/root/a", Size: 1, SizeKnown: true}},
	}
	mc := NewAnalyzeModel(AnalyzeConfig{Root: complete, Color: false})
	if got := mc.header(); strings.Contains(got, "partial scan") {
		t.Fatalf("complete scan should not warn: %q", got)
	}
}

func TestAnalyzeHeaderWarnsOnSkippedMounts(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{{Name: "a", Path: "/root/a", Size: 1, SizeKnown: true}},
		SkippedMounts: []scan.SkippedMount{{
			Path: "/root/other", Source: "/dev/sdb1", Fstype: "ext4",
		}},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
	if got := m.header(); !strings.Contains(got, "skipped 1 mount") || !strings.Contains(got, "/root/other") {
		t.Fatalf("header missing skipped-mount warning: %q", got)
	}
	complete := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{{Name: "a", Path: "/root/a", Size: 1, SizeKnown: true}},
	}
	mc := NewAnalyzeModel(AnalyzeConfig{Root: complete, Color: false})
	if got := mc.header(); strings.Contains(got, "skipped") {
		t.Fatalf("complete scan should not warn about mounts: %q", got)
	}
}

func TestAnalyzeNavigation(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{
			{Name: "a", Path: "/root/a", IsDir: false, Size: 10, SizeKnown: true},
			{Name: "b", Path: "/root/b", IsDir: true, Size: 20, SizeKnown: true,
				Children: []*scan.Node{{Name: "c", Path: "/root/b/c", IsDir: false, Size: 5, SizeKnown: true}}},
		},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
	if len(m.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(m.entries))
	}
	// Sorted by size desc: b (20) then a (10).
	if m.entries[0].Name != "b" {
		t.Fatalf("first entry = %s, want b", m.entries[0].Name)
	}
	m.enter()
	if m.dir.Path != "/root/b" {
		t.Fatalf("enter moved to %s, want /root/b", m.dir.Path)
	}
	if len(m.entries) != 1 || m.entries[0].Name != "c" {
		t.Fatalf("entered entries = %+v", m.entries)
	}
	m.parent()
	if m.dir.Path != "/root" {
		t.Fatalf("parent moved to %s, want /root", m.dir.Path)
	}
}

// TestAnalyzeKeysAreReadOnly asserts the explorer exposes no delete or
// selection keys: d, D, space, and a must not change the model's state.
func TestAnalyzeKeysAreReadOnly(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{{Name: "a", Path: "/root/a", Size: 10, SizeKnown: true}},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
	keys := []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("D")},
		tea.KeyMsg{Type: tea.KeySpace},
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")},
	}
	for _, k := range keys {
		updated, _ := m.Update(k)
		mm := updated.(*AnalyzeModel)
		if got := mm.footer(); strings.Contains(got, "trash") || strings.Contains(got, "confirm") {
			t.Fatalf("key %v touched a delete affordance: %q", k, got)
		}
		if view := mm.View(); strings.Contains(view, "[y]") {
			t.Fatalf("key %v opened a delete confirmation: %q", k, view)
		}
	}
}

// --- shared test helpers ---

func pressKey(t *testing.T, m tea.Model, msg tea.Msg) tea.Cmd {
	t.Helper()
	updated, cmd := m.Update(msg)
	if updated == nil {
		t.Fatalf("model became nil after %v", msg)
	}
	return cmd
}

func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// TestAnalyzeHelpThenQuit pins the overlay behaviour: q closes the help and the
// next q quits the program.
func TestAnalyzeHelpThenQuit(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{{Name: "a", Path: "/root/a", Size: 10, SizeKnown: true}},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
	if cmd := pressKey(t, m, runeKey('?')); cmd != nil {
		t.Fatalf("opening help ran a command: %v", cmd)
	}
	if !m.help.ShowAll {
		t.Fatal("? should open the full help")
	}
	if cmd := pressKey(t, m, runeKey('q')); cmd != nil {
		t.Fatal("q while help is open must close the help, not quit")
	}
	if m.help.ShowAll {
		t.Fatal("q should close the help overlay")
	}
	if cmd := pressKey(t, m, runeKey('q')); !isQuitCmd(cmd) {
		t.Fatal("the q after closing help must quit")
	}
}

func TestAnalyzeHelpThenEscThenQuit(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{{Name: "a", Path: "/root/a", Size: 10, SizeKnown: true}},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
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

// TestAnalyzeEnterAndRightDescend covers the shared keymap: enter, → and l all
// enter the directory under the cursor; ← and h go back.
func TestAnalyzeEnterAndRightDescend(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{
			{Name: "a", Path: "/root/a", Size: 10, SizeKnown: true},
			{Name: "b", Path: "/root/b", IsDir: true, Size: 20, SizeKnown: true,
				Children: []*scan.Node{{Name: "c", Path: "/root/b/c", Size: 5, SizeKnown: true}}},
		},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
	// Size sort puts b (20) first.
	if cmd := pressKey(t, m, tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
		t.Fatal("enter should not run a command")
	}
	if m.dir.Path != "/root/b" {
		t.Fatalf("enter moved to %s, want /root/b", m.dir.Path)
	}
	pressKey(t, m, runeKey('h'))
	if m.dir != root {
		t.Fatalf("h moved to %s, want the root", m.dir.Path)
	}
	pressKey(t, m, tea.KeyMsg{Type: tea.KeyRight})
	if m.dir.Path != "/root/b" {
		t.Fatalf("\u2192 moved to %s, want /root/b", m.dir.Path)
	}
	pressKey(t, m, runeKey('h'))
	pressKey(t, m, runeKey('l'))
	if m.dir.Path != "/root/b" {
		t.Fatalf("l moved to %s, want /root/b", m.dir.Path)
	}
}

// TestAnalyzeSortKeepsCursor asserts a sort never moves the cursor to a
// different entry.
func TestAnalyzeSortKeepsCursor(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{
			{Name: "a", Path: "/root/a", Size: 10, SizeKnown: true, ItemCount: 3,
				Mtime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{Name: "b", Path: "/root/b", Size: 30, SizeKnown: true, ItemCount: 1,
				Mtime: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)},
			{Name: "c", Path: "/root/c", Size: 20, SizeKnown: true, ItemCount: 9,
				Mtime: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
	// Size sort: b (30), c (20), a (10).
	m.setCursor(1)
	cur := m.entries[m.cursor]
	if cur.Name != "c" {
		t.Fatalf("cursor on %s, want c", cur.Name)
	}
	for _, s := range []SortMode{SortByName, SortByMtime, SortByCount, SortBySize} {
		pressKey(t, m, runeKey(sortKey(s)))
		if m.sort != s {
			t.Fatalf("sort mode = %v, want %v", m.sort, s)
		}
		if m.entries[m.cursor] != cur {
			t.Fatalf("sort %v moved the cursor from %s to %s", s, cur.Name, m.entries[m.cursor].Name)
		}
	}
}

// sortKey maps a sort mode to the key the shared keymap assigns it.
func sortKey(s SortMode) rune {
	switch s {
	case SortByName:
		return 'n'
	case SortByMtime:
		return 't'
	case SortByCount:
		return 'c'
	default:
		return 's'
	}
}

// TestAnalyzeRowShowsMtimeAndShare asserts the row renders the columns the sorts
// order by: mtime and the entry's share of its directory.
func TestAnalyzeRowShowsMtimeAndShare(t *testing.T) {
	child := &scan.Node{
		Name: "data", Path: "/root/data", IsDir: true, Size: 40, SizeKnown: true,
		Mtime: time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC),
	}
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, Size: 100, SizeKnown: true,
		Children: []*scan.Node{child},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
	m.width = 120
	row := m.row(child, false, 100, m.rowLayout())
	if !strings.Contains(row, "Mar 04 05:06") {
		t.Fatalf("row missing the mtime column: %q", row)
	}
	if !strings.Contains(row, "40.0%") {
		t.Fatalf("row missing its share of the directory: %q", row)
	}
}

// TestAnalyzeHeaderShowsDirTotalAndParentShare asserts the header states the
// current directory's size and its share of the parent.
func TestAnalyzeHeaderShowsDirTotalAndParentShare(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, Size: 100, SizeKnown: true,
		Children: []*scan.Node{{
			Name: "data", Path: "/root/data", IsDir: true, Size: 40, SizeKnown: true,
			Children: []*scan.Node{{Name: "f", Path: "/root/data/f", Size: 40, SizeKnown: true}},
		}},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
	m.enter()
	header := m.header()
	if !strings.Contains(header, "this dir:") {
		t.Fatalf("header missing the directory total: %q", header)
	}
	if !strings.Contains(header, "40.0% of parent") {
		t.Fatalf("header missing the parent share: %q", header)
	}
}

// TestAnalyzeRowsFitNarrowWidths asserts the optional columns are dropped rather
// than overflowing the terminal.
func TestAnalyzeRowsFitNarrowWidths(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, Size: 1000, SizeKnown: true,
		Children: []*scan.Node{
			{Name: "a-very-long-directory-name-that-would-overflow", Path: "/root/a",
				IsDir: true, Size: 900, SizeKnown: true,
				Mtime: time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC)},
			{Name: "b", Path: "/root/b", Size: 100, SizeKnown: true,
				Mtime: time.Date(2026, 3, 5, 5, 6, 0, 0, time.UTC)},
		},
	}
	for _, width := range []int{120, 80, 60, 40, 30} {
		m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
		m.width = width
		m.height = 24
		for _, line := range strings.Split(m.list(), "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Fatalf("width %d: row is %d cells: %q", width, got, line)
			}
		}
	}
}

// TestAnalyzeFooterListsEveryBinding asserts the short footer lists all bindings
// at a normal width, and degrades to a truthful "? all keys" hint when narrow.
func TestAnalyzeFooterListsEveryBinding(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{{Name: "a", Path: "/root/a", Size: 10, SizeKnown: true}},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false})
	m.width = 120
	footer := m.footer()
	for _, want := range []string{
		"up", "down", "open dir", "back", "page up", "page down", "top", "bottom",
		"sort size", "sort name", "sort mtime", "sort count", "filter", "all keys", "quit",
	} {
		if !strings.Contains(footer, want) {
			t.Fatalf("footer hides %q:\n%s", want, footer)
		}
	}
	if lines := strings.Count(footer, "\n") + 1; lines > 2 {
		t.Fatalf("footer is %d lines, want at most 2:\n%s", lines, footer)
	}

	m.width = 40
	narrow := m.footer()
	if !strings.Contains(narrow, "? all keys") {
		t.Fatalf("narrow footer must point at the full help:\n%s", narrow)
	}
	if lines := strings.Count(narrow, "\n") + 1; lines > 2 {
		t.Fatalf("narrow footer is %d lines, want at most 2:\n%s", lines, narrow)
	}
}
