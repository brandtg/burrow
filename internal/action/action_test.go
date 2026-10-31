package action_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brandtg/burrow/internal/action"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
)

func TestTrashActionMovesToTrash(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cache-item")
	if err := os.WriteFile(src, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := safe.TakeSnapshot(src)
	if err != nil {
		t.Fatal(err)
	}
	tr := trash.New(filepath.Join(dir, "Trash"), os.Getuid())
	a := action.NewTrash(src, tr, snap, nil)

	res, err := a.Run(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != engine.StatusOK {
		t.Fatalf("status = %q", res.Status)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still present: %v", err)
	}
	entries, err := tr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path != src {
		t.Fatalf("trash entries = %+v", entries)
	}
}

func TestTrashActionIdentityGuard(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "victim")
	if err := os.WriteFile(src, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := safe.TakeSnapshot(src)
	if err != nil {
		t.Fatal(err)
	}
	// Swap the path between snapshot and execution.
	if err := os.Rename(src, src+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("attacker"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := trash.New(filepath.Join(dir, "Trash"), os.Getuid())
	a := action.NewTrash(src, tr, snap, nil)

	_, err = a.Run(context.Background(), false)
	var gs *safe.GuardStopError
	if !errors.As(err, &gs) {
		t.Fatalf("expected guard stop, got %v", err)
	}
	if _, statErr := os.Stat(src); statErr != nil {
		t.Fatalf("swapped path must not be deleted: %v", statErr)
	}
}

func TestTrashActionHonoursCancelledContext(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cache-item")
	if err := os.WriteFile(src, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := safe.TakeSnapshot(src)
	if err != nil {
		t.Fatal(err)
	}
	tr := trash.New(filepath.Join(dir, "Trash"), os.Getuid())
	a := action.NewTrash(src, tr, snap, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Run(ctx, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if _, err := os.Lstat(src); err != nil {
		t.Fatalf("cancelled action must leave the source untouched: %v", err)
	}
	if entries, _ := tr.List(); len(entries) != 0 {
		t.Fatalf("cancelled action created %d trash entries", len(entries))
	}
}

func TestTrashActionIdempotentWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "gone")
	a := action.NewTrash(missing, trash.New(filepath.Join(dir, "Trash"), os.Getuid()), nil, nil)
	res, err := a.Run(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != engine.StatusOK {
		t.Fatalf("expected ok for already-absent path, got %q", res.Status)
	}
}

func TestOwnerCommandActionDryRun(t *testing.T) {
	cmd := owner.AptPackageCache()
	a := action.NewOwnerCommand(cmd, owner.New(true))
	res, err := a.Run(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != engine.StatusDryRun {
		t.Fatalf("status = %q", res.Status)
	}
	if strings.Join(res.Argv, " ") != "sudo -- apt-get clean" {
		t.Fatalf("argv = %v", res.Argv)
	}
	if a.Recovery() != engine.RecoveryOwner {
		t.Fatalf("recovery = %v", a.Recovery())
	}
}

func TestOwnerCommandActionFailure(t *testing.T) {
	cmd := owner.OwnerCommand{ID: "x", Argv: []string{"/bin/false"}}
	a := action.NewOwnerCommand(cmd, owner.New(false))
	res, err := a.Run(context.Background(), false)
	if err == nil {
		t.Fatal("expected error from a failing command")
	}
	if res.Status != engine.StatusFailed {
		t.Fatalf("status = %q", res.Status)
	}
}

func TestEmptyTrashAction(t *testing.T) {
	dir := t.TempDir()
	tr := trash.New(filepath.Join(dir, "Trash"), os.Getuid())
	src := filepath.Join(dir, "junk")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Put(src); err != nil {
		t.Fatal(err)
	}
	a := &action.EmptyTrashAction{Trash: tr, Path: tr.Home()}
	if a.Recovery() != engine.RecoveryOwner {
		t.Fatalf("recovery = %v", a.Recovery())
	}
	if _, err := a.Run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if entries, _ := tr.List(); len(entries) != 1 {
		t.Fatal("dry-run must not empty the trash")
	}
	if _, err := a.Run(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if entries, _ := tr.List(); len(entries) != 0 {
		t.Fatal("trash was not emptied")
	}
}
