package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brandtg/burrow/internal/scan"
	tea "github.com/charmbracelet/bubbletea"
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
