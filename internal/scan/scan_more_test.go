package scan_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/brandtg/burrow/internal/scan"
)

func TestScanProgressCallback(t *testing.T) {
	root := t.TempDir()
	for i := range 10 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("f%d", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var calls int
	if _, err := scan.Analyze(context.Background(), root, scan.Options{
		Progress: func(scan.Progress) { calls++ },
	}); err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("progress callback was never invoked")
	}
}

func TestHardlinksNotDoubleCounted(t *testing.T) {
	root := t.TempDir()
	content := make([]byte, 256*1024)
	a := filepath.Join(root, "a")
	if err := os.WriteFile(a, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(a, filepath.Join(root, "b")); err != nil {
		t.Skipf("hardlinks unsupported here: %v", err)
	}

	n, err := scan.Analyze(context.Background(), root, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	// The shared inode must count once: apparent size is one copy, not two.
	if n.Apparent != int64(len(content)) {
		t.Fatalf("apparent = %d, want %d (hardlink double-counted)", n.Apparent, len(content))
	}
}

func TestPartialUnreadableSubtree(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read an unreadable directory")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	n, err := scan.Analyze(context.Background(), root, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := scan.Status(n); got != scan.StatusPartial {
		t.Fatalf("status = %q, want %q", got, scan.StatusPartial)
	}
	if got := scan.UnknownCount(n); got != 1 {
		t.Fatalf("UnknownCount = %d, want 1", got)
	}
}

func TestUnknownCountZeroWhenComplete(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := scan.Analyze(context.Background(), root, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := scan.UnknownCount(n); got != 0 {
		t.Fatalf("UnknownCount = %d, want 0", got)
	}
}

func TestSingleFileScan(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(f, make([]byte, 1234), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := scan.Analyze(context.Background(), f, scan.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if n.IsDir {
		t.Fatal("file scanned as directory")
	}
	if n.Apparent != 1234 {
		t.Fatalf("apparent = %d, want 1234", n.Apparent)
	}
	if !n.SizeKnown {
		t.Fatal("single file size should be known")
	}
}

func TestDepthLimit(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "c"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := scan.Analyze(context.Background(), root, scan.Options{Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Children) != 1 {
		t.Fatalf("depth 1 children = %d, want 1", len(n.Children))
	}
	if len(n.Children[0].Children) != 0 {
		t.Fatalf("depth 1 should not include grandchildren, got %d", len(n.Children[0].Children))
	}
}

func TestNoHidden(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".secret"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible"), []byte("v"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := scan.Analyze(context.Background(), root, scan.Options{NoHidden: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range n.Children {
		if c.Name == ".secret" {
			t.Fatal("--no-hidden did not skip the dotfile")
		}
	}
}
