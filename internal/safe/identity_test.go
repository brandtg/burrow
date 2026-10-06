package safe_test

import (
	"errors"
	"os"
	"path/filepath"
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
