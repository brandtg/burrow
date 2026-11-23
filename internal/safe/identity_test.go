package safe_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brandtg/burrow/internal/safe"
)

func TestSnapshotIdentitySwapDetected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "victim")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := safe.TakeSnapshot(target)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := snap.Matches(); err != nil || !ok {
		t.Fatalf("expected initial match, ok=%v err=%v", ok, err)
	}

	// Swap: rename the original away and put a different inode in its place.
	if err := os.Rename(target, target+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("replacement"), 0o644); err != nil {
		t.Fatal(err)
	}

	ok, err := snap.Matches()
	if ok {
		t.Fatal("expected identity mismatch after swap")
	}
	var v *safe.Violation
	if !errors.As(err, &v) {
		t.Fatalf("expected Violation, got %v", err)
	}
}

func TestSnapshotParentSwapDetected(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "parent")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "victim")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := safe.TakeSnapshot(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, dir+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := snap.Matches(); ok {
		t.Fatal("expected parent identity mismatch")
	}
}

func TestSnapshotMissingPathFailsClosed(t *testing.T) {
	if _, err := safe.TakeSnapshot(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing path")
	}
}

// TestSnapshotInPlaceRewriteDetected pins the same-inode rule: rewriting the
// file's bytes in place (same dev/ino) is a mutation the guard must refuse —
// dev/ino alone cannot see it.
func TestSnapshotInPlaceRewriteDetected(t *testing.T) {
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := safe.TakeSnapshot(target)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("tampered"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	ok, err := snap.Matches()
	if ok {
		t.Fatal("an in-place rewrite must not match the snapshot")
	}
	var v *safe.Violation
	if !errors.As(err, &v) || !strings.Contains(v.Reason, "content or mode") {
		t.Fatalf("expected a content-change violation, got %v", err)
	}
}

// TestSnapshotModeChangeDetected pins the mode half: a permission change
// between snapshot and execution is refused.
func TestSnapshotModeChangeDetected(t *testing.T) {
	target := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := safe.TakeSnapshot(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, _ := snap.Matches(); ok {
		t.Fatal("a mode change must not match the snapshot")
	}
}
