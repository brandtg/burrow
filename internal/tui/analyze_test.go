package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/scan"
	"github.com/brandtg/burrow/internal/trash"
)

func TestTrashExecIdentitySwapGuardStop(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "victim")
	if err := os.WriteFile(victim, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := safe.TakeSnapshot(victim)
	if err != nil {
		t.Fatal(err)
	}
	// Swap the path for a different inode between selection and execution.
	// Rename the original away rather than deleting it so its inode stays
	// allocated and the replacement cannot reuse it; ext4 reuses freed inodes.
	if err := os.Rename(victim, victim+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := &TrashExec{
		Trash:     trash.New(filepath.Join(base, "Trash"), os.Getuid()),
		Validator: safe.NewValidator(os.Getuid()),
	}
	err = exec.MoveToTrash(snap, 8, victim)
	if err == nil {
		t.Fatal("expected a guard-stop for a swapped path")
	}
	var gs *safe.GuardStopError
	if !errors.As(err, &gs) {
		t.Fatalf("want guard-stop, got %T: %v", err, err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("swapped path should be left alone: %v", err)
	}
}

func TestTrashExecJournalAndRestore(t *testing.T) {
	base := t.TempDir()
	trashDir := filepath.Join(base, "data", "Trash")
	victim := filepath.Join(base, "victim")
	if err := os.WriteFile(victim, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(base, "state", "operations.jsonl")
	jw, err := journal.NewWriter(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	exec := &TrashExec{
		Trash:     trash.New(trashDir, os.Getuid()),
		Validator: safe.NewValidator(os.Getuid()),
		Journal:   jw,
		Command:   "analyze",
	}
	snap, err := safe.TakeSnapshot(victim)
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.MoveToTrash(snap, 5, victim); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(victim); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("victim should be gone, stat err = %v", err)
	}

	records, err := journal.Read(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var ok int
	for _, r := range records {
		if r.Action == journal.ActionTrash && r.Status == journal.StatusOK && r.Path == victim {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("expected one ok trash record, got %d (%+v)", ok, records)
	}

	tr := trash.New(trashDir, os.Getuid())
	entries, err := tr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path != victim {
		t.Fatalf("trash entries = %+v", entries)
	}
	if err := tr.Restore(entries[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("restore did not bring the item back: %v", err)
	}
}

func TestTrashExecRefusesProtectedRoot(t *testing.T) {
	exec := &TrashExec{
		Trash:     trash.New(filepath.Join(t.TempDir(), "Trash"), os.Getuid()),
		Validator: safe.NewValidator(os.Getuid()),
	}
	snap, err := safe.TakeSnapshot("/etc")
	if err != nil {
		t.Skipf("cannot snapshot /etc: %v", err)
	}
	if err := exec.MoveToTrash(snap, 0, "/etc"); err == nil {
		t.Fatal("expected protected path to be refused")
	}
}

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
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false, UID: os.Getuid()})
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

func TestRemoveNodeSubtractsSubtreeItemCount(t *testing.T) {
	root := &scan.Node{Name: "root", Path: "/root", IsDir: true, SizeKnown: true, ItemCount: 5, Size: 110}
	zulu := &scan.Node{Name: "zulu", Path: "/root/zulu", IsDir: true, SizeKnown: true, ItemCount: 2, Size: 100}
	root.Children = []*scan.Node{zulu}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false, UID: os.Getuid()})
	m.removeNode(zulu)
	if root.ItemCount != 3 {
		t.Fatalf("root ItemCount = %d, want 3 (subtract the whole subtree)", root.ItemCount)
	}
	if len(root.Children) != 0 {
		t.Fatalf("root still has %d children", len(root.Children))
	}
	if root.Size != 10 {
		t.Fatalf("root Size = %d, want 10", root.Size)
	}
}

func TestNoDeleteDisablesTrashKeys(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{{Name: "a", Path: "/root/a", IsDir: false, Size: 10, SizeKnown: true}},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false, NoDelete: true, UID: os.Getuid()})
	m.beginDelete(false)
	if m.confirm {
		t.Fatal("delete confirmation should be disabled under --no-delete")
	}
	if m.status == "" {
		t.Fatal("expected a status message explaining the disabled keys")
	}
}

func TestAnalyzeBackgroundLoadShowsProgress(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{{Name: "a", Path: "/root/a", Size: 10, SizeKnown: true}},
	}
	m := NewAnalyzeModel(AnalyzeConfig{
		Load:  func(context.Context, func(scan.Progress)) (*scan.Node, error) { return root, nil },
		Color: false, UID: os.Getuid(),
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
		Color: false, UID: os.Getuid(),
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

func TestAnalyzeModelNavigationAndSelection(t *testing.T) {
	root := &scan.Node{
		Name: "root", Path: "/root", IsDir: true, SizeKnown: true,
		Children: []*scan.Node{
			{Name: "a", Path: "/root/a", IsDir: false, Size: 10, SizeKnown: true},
			{Name: "b", Path: "/root/b", IsDir: true, Size: 20, SizeKnown: true,
				Children: []*scan.Node{{Name: "c", Path: "/root/b/c", IsDir: false, Size: 5, SizeKnown: true}}},
		},
	}
	m := NewAnalyzeModel(AnalyzeConfig{Root: root, Color: false, UID: os.Getuid()})
	if len(m.entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(m.entries))
	}
	// Sorted by size desc: b (20) then a (10).
	if m.entries[0].Name != "b" {
		t.Fatalf("first entry = %s, want b", m.entries[0].Name)
	}
	m.toggleSelect()
	if !m.selected["/root/b"] {
		t.Fatal("space did not select the cursor row")
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
