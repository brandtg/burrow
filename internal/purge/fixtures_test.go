package purge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("..", "..", "testdata", "purge", name)
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, p)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	touchTree(t, dst, time.Now().Add(-30*24*time.Hour))
	return dst
}

func TestFixtureTrackedKept(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := copyFixture(t, "tracked")
	cmd := exec.Command("git", "-C", root, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	cmd = exec.Command("git", "-C", root, "add", "build/keep.txt")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	got, err := Discover(context.Background(), Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Kept() {
		t.Fatalf("tracked fixture must be kept: %+v", got)
	}
}

func TestFixtureUntrackedPurged(t *testing.T) {
	root := copyFixture(t, "untracked")
	got, err := Discover(context.Background(), Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kept() {
		t.Fatalf("untracked fixture must be purgeable: %+v", got)
	}
}

func TestFixtureNestedRepoKept(t *testing.T) {
	root := copyFixture(t, "nestedrepo")
	if err := os.MkdirAll(filepath.Join(root, "build", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "build", ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(context.Background(), Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Kept() {
		t.Fatalf("nested-repo fixture must be kept: %+v", got)
	}
}
