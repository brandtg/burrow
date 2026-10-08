//go:build linux

package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func cloneFile(t *testing.T, src, dst string) error {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	return unix.IoctlFileClone(int(out.Fd()), int(in.Fd()))
}

func TestReflinkSharedExtentsNotDoubleCounted(t *testing.T) {
	dir := t.TempDir()
	if !sharedExtentFS(dir) {
		t.Skip("temp filesystem does not support reflinks")
	}
	content := make([]byte, 512*1024)
	for i := range content {
		content[i] = byte(i * 7)
	}
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "copy.bin")
	if err := cloneFile(t, src, dst); err != nil {
		t.Skipf("reflink unsupported here: %v", err)
	}
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	oneCopy := fi.Size()

	n, err := Analyze(context.Background(), dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// The tree references the extents once, so the total is about one copy, not
	// two. Allow a block of slack for the directory entry and rounding.
	if n.Size > oneCopy+oneCopy/4 {
		t.Fatalf("reflinked tree size = %d, want about one copy (%d)", n.Size, oneCopy)
	}
	if len(n.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(n.Children))
	}
	for _, c := range n.Children {
		if c.Size > oneCopy {
			t.Fatalf("%s size = %d, want at most one copy (%d)", c.Name, c.Size, oneCopy)
		}
	}
}
