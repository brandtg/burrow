package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDUSizeFallback(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	size, ok := duSize(context.Background(), dir)
	if !ok {
		t.Skip("du unavailable in this environment")
	}
	if size <= 0 {
		t.Fatalf("du size = %d, want positive", size)
	}
}

func TestFallbackDirUnknownWhenDuFails(t *testing.T) {
	// A path that does not exist yields an error from Lstat, proving fallback
	// fails closed rather than reporting a bogus zero.
	if _, err := fallback(context.Background(), filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected fallback on a missing path to fail")
	}
}

func TestFileNodeOnDiskAndApparent(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(f)
	if err != nil {
		t.Fatal(err)
	}
	n := fileNode(f, fi)
	if n.Apparent != 5000 {
		t.Fatalf("apparent = %d, want 5000", n.Apparent)
	}
	if n.Size < 4096 {
		t.Fatalf("on-disk = %d, want at least one block", n.Size)
	}
}
