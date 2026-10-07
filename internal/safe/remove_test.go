package safe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveTreeDeletes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "artifact")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap, err := TakeSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveTree(dir, DefaultValidator, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("expected removal, stat err = %v", err)
	}
}

func TestRemoveTreeNilSnapshotFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if err := RemoveTree(dir, DefaultValidator, nil); err == nil {
		t.Fatal("expected a guard-stop for a nil snapshot")
	}
	if _, err := os.Lstat(dir); err != nil {
		t.Fatalf("nil snapshot must not delete: %v", err)
	}
}

func TestRemoveTreeValidatorRejects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "artifact")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snap, err := TakeSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	v := &Validator{UID: -1, Roots: []string{"/nonexistent"}, RequireRoot: true}
	if err := RemoveTree(dir, v, snap); err == nil {
		t.Fatal("expected the allow-list to reject the path")
	}
	if _, err := os.Lstat(dir); err != nil {
		t.Fatalf("validator rejection must not delete: %v", err)
	}
}

func TestRemoveTreeChangedIdentityFailsClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "artifact")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snap, err := TakeSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := RemoveTree(dir, DefaultValidator, snap); err == nil {
		t.Fatal("expected a guard-stop for a changed identity")
	}
}
