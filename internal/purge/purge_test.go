package purge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/safe"
)

func TestMatchArtifact(t *testing.T) {
	patterns := []string{"node_modules", "target", ".next/cache", "build"}
	cases := []struct {
		rel  string
		want string
		ok   bool
	}{
		{"node_modules", "node_modules", true},
		{"pkg/node_modules", "node_modules", true},
		{"a/b/node_modules", "node_modules", true},
		{"target", "target", true},
		{"foo/.next/cache", ".next/cache", true},
		{".next/cache", ".next/cache", true},
		{"foo/.next/build", "build", true},
		{"build", "build", true},
		{"node_modules_extra", "", false},
	}
	for _, c := range cases {
		got, ok := matchArtifact(c.rel, patterns)
		if got != c.want || ok != c.ok {
			t.Fatalf("matchArtifact(%q) = (%q,%v), want (%q,%v)", c.rel, got, ok, c.want, c.ok)
		}
	}
}

// makeArtifact creates dir/<name>/file with content and an old mtime.
func makeArtifact(t *testing.T, dir, name string, when time.Time) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	touchTree(t, p, when)
	return p
}

func touchTree(t *testing.T, root string, when time.Time) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, when, when)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverUntrackedPurged(t *testing.T) {
	root := t.TempDir()
	art := makeArtifact(t, root, "node_modules", time.Now().Add(-30*24*time.Hour))
	got, err := Discover(context.Background(), Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 artifact, got %d: %+v", len(got), got)
	}
	if got[0].Kept() {
		t.Fatalf("untracked artifact should be purgeable, reason=%q", got[0].KeepReason)
	}
	if got[0].Path != art {
		t.Fatalf("path = %q, want %q", got[0].Path, art)
	}
}

func TestDiscoverPrunesNestedArtifacts(t *testing.T) {
	root := t.TempDir()
	outer := makeArtifact(t, root, "node_modules", time.Now().Add(-30*24*time.Hour))
	inner := filepath.Join(outer, "nested", "node_modules")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(context.Background(), Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != outer {
		t.Fatalf("discovery must prune into a matched artifact: %+v", got)
	}
}

func TestDiscoverNestedRepoKept(t *testing.T) {
	root := t.TempDir()
	art := makeArtifact(t, root, "build", time.Now().Add(-30*24*time.Hour))
	if err := os.MkdirAll(filepath.Join(art, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(context.Background(), Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Kept() {
		t.Fatalf("artifact containing a git repo must be kept: %+v", got)
	}
}

// fakeGit simulates git for tracked/unknown tests.
type fakeGit struct {
	root      string
	tracked   []string
	err       error
	commit    time.Time
	commitErr error
}

func (f fakeGit) TopLevel(context.Context, string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.root, nil
}

func (f fakeGit) LsFiles(context.Context, string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tracked, nil
}

func (f fakeGit) LastCommit(context.Context, string) (time.Time, error) {
	if f.commitErr != nil {
		return time.Time{}, f.commitErr
	}
	return f.commit, nil
}

func TestDiscoverGitTrackedKept(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	art := makeArtifact(t, root, "node_modules", time.Now().Add(-30*24*time.Hour))
	if err := os.MkdirAll(filepath.Join(art, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	git := fakeGit{root: root, tracked: []string{"node_modules/keep/file.txt"}}
	got, err := Discover(context.Background(), Options{Roots: []string{root}, Git: git})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Tracked || !got[0].Kept() {
		t.Fatalf("git-tracked artifact must be kept: %+v", got)
	}
	if got[0].Project != root {
		t.Fatalf("project = %q, want repo root %q", got[0].Project, root)
	}
}

func TestDiscoverUnknownGitKept(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeArtifact(t, root, "node_modules", time.Now().Add(-30*24*time.Hour))
	git := fakeGit{err: errors.New("git unavailable")}
	got, err := Discover(context.Background(), Options{Roots: []string{root}, Git: git})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Kept() {
		t.Fatalf("unknown git state must keep the artifact: %+v", got)
	}
}

func TestDiscoverAgeFilter(t *testing.T) {
	root := t.TempDir()
	makeArtifact(t, root, "stale", time.Now().Add(-30*24*time.Hour))
	makeArtifact(t, root, "fresh", time.Now())
	got, err := Discover(context.Background(), Options{
		Roots:        []string{root},
		ArtifactDirs: []string{"stale", "fresh"},
		MaxAge:       7 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Artifact{}
	for _, a := range got {
		byName[filepath.Base(a.Path)] = a
	}
	if byName["stale"].Kept() {
		t.Fatalf("stale artifact should be purgeable: %+v", byName["stale"])
	}
	if !byName["fresh"].Kept() {
		t.Fatalf("recently-active artifact must be kept: %+v", byName["fresh"])
	}
}

func TestDiscoverWarnsOnMissingRoot(t *testing.T) {
	root := t.TempDir()
	makeArtifact(t, root, "node_modules", time.Now().Add(-30*24*time.Hour))
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	var warnings []string
	got, err := Discover(context.Background(), Options{
		Roots: []string{missing, root},
		Warn:  func(msg string) { warnings = append(warnings, msg) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the good root to still be scanned, got %d artifacts", len(got))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for the missing root, got %v", warnings)
	}
}

func TestDiscoverDepthBound(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b", "c", "d", "node_modules")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(context.Background(), Options{Roots: []string{root}, MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("depth bound should exclude the deep artifact: %+v", got)
	}
}

func TestDiscoverActiveProjectKept(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeArtifact(t, root, "node_modules", time.Now().Add(-60*24*time.Hour))
	git := fakeGit{root: root, commit: time.Now().Add(-24 * time.Hour)}
	got, err := Discover(context.Background(), Options{
		Roots:        []string{root},
		Git:          git,
		ActiveWithin: 30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Kept() {
		t.Fatalf("artifact in a recently-committed repo must be kept: %+v", got)
	}
	if !strings.Contains(got[0].KeepReason, "active project") {
		t.Fatalf("keep reason = %q, want active project", got[0].KeepReason)
	}
}

func TestDiscoverPerTypeAge(t *testing.T) {
	root := t.TempDir()
	makeArtifact(t, root, "node_modules", time.Now().Add(-20*24*time.Hour))
	makeArtifact(t, root, "__pycache__", time.Now().Add(-20*24*time.Hour))
	got, err := Discover(context.Background(), Options{
		Roots:        []string{root},
		ArtifactDirs: []string{"node_modules", "__pycache__"},
		MaxAge:       7 * 24 * time.Hour,
		MaxAgeByType: map[string]time.Duration{
			"node_modules": 30 * 24 * time.Hour,
			"__pycache__":  0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Artifact{}
	for _, a := range got {
		byName[filepath.Base(a.Path)] = a
	}
	if !byName["node_modules"].Kept() {
		t.Fatalf("node_modules within its 30d window must be kept: %+v", byName["node_modules"])
	}
	if byName["__pycache__"].Kept() {
		t.Fatalf("__pycache__ with a zero window must be purgeable: %+v", byName["__pycache__"])
	}
}

func TestDiscoverGenericNameRequiresMarker(t *testing.T) {
	root := t.TempDir()
	makeArtifact(t, root, "build", time.Now().Add(-30*24*time.Hour))

	got, err := Discover(context.Background(), Options{
		Roots:        []string{root},
		ArtifactDirs: []string{"build"},
		MaxAge:       7 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].Kept() || !strings.Contains(got[0].KeepReason, "project marker") {
		t.Fatalf("unmarked build dir must be kept: %+v", got)
	}

	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = Discover(context.Background(), Options{
		Roots:        []string{root},
		ArtifactDirs: []string{"build"},
		MaxAge:       7 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kept() {
		t.Fatalf("build dir with a project marker should be purgeable: %+v", got)
	}
}

func TestExecutorDeleteAndJournal(t *testing.T) {
	root := t.TempDir()
	art := makeArtifact(t, root, "node_modules", time.Now().Add(-30*24*time.Hour))
	arts, err := Discover(context.Background(), Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 {
		t.Fatalf("expected 1 artifact, got %d", len(arts))
	}
	journalPath := filepath.Join(t.TempDir(), "operations.jsonl")
	w, err := journal.NewWriter(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	exec := &Executor{Journal: w, Command: "purge"}
	freed, err := exec.Delete(&arts[0], false)
	if err != nil {
		t.Fatal(err)
	}
	if freed <= 0 {
		t.Fatalf("expected freed bytes > 0, got %d", freed)
	}
	if _, err := os.Lstat(art); !os.IsNotExist(err) {
		t.Fatalf("artifact should be deleted, stat err = %v", err)
	}
	if err := exec.EndRun(freed); err != nil {
		t.Fatal(err)
	}
	records, err := journal.Read(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range records {
		if r.Action == journal.ActionPurge {
			found = true
			if r.Recovery != journal.RecoveryOwner {
				t.Fatalf("recovery = %q, want owner", r.Recovery)
			}
			if r.Status != journal.StatusOK {
				t.Fatalf("status = %q, want ok", r.Status)
			}
		}
	}
	if !found {
		t.Fatal("no purge record in journal")
	}
}

func TestExecutorDryRunDoesNotDelete(t *testing.T) {
	root := t.TempDir()
	art := makeArtifact(t, root, "node_modules", time.Now().Add(-30*24*time.Hour))
	arts, err := Discover(context.Background(), Options{Roots: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	exec := &Executor{Command: "purge"}
	if _, err := exec.Delete(&arts[0], true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(art); err != nil {
		t.Fatalf("dry-run must not delete: %v", err)
	}
}

func TestRemoveTreeRejectsChangedIdentity(t *testing.T) {
	root := t.TempDir()
	art := makeArtifact(t, root, "node_modules", time.Now())
	snap, err := safe.TakeSnapshot(art)
	if err != nil {
		t.Fatal(err)
	}
	// Replace the artifact with a different directory. Rename the original
	// away rather than deleting it so its inode stays allocated and the
	// replacement cannot reuse it; ext4 reuses freed inodes.
	if err := os.Rename(art, art+".moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(art, 0o755); err != nil {
		t.Fatal(err)
	}
	v := safe.NewValidator(os.Getuid())
	if err := safe.RemoveTree(art, v, snap); err == nil {
		t.Fatal("expected a guard-stop for a changed identity")
	}
}

// TestDiscoverRealGit exercises the real git integration when git is installed.
func TestDiscoverRealGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.MkdirAll(filepath.Join(root, "tracked-pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked-pkg", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked-pkg/keep.txt")
	makeArtifact(t, root, "tracked-pkg", time.Now().Add(-30*24*time.Hour))
	makeArtifact(t, root, "node_modules", time.Now().Add(-30*24*time.Hour))

	got, err := Discover(context.Background(), Options{
		Roots:        []string{root},
		ArtifactDirs: []string{"tracked-pkg", "node_modules"},
	})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Artifact{}
	for _, a := range got {
		byName[filepath.Base(a.Path)] = a
	}
	if !byName["tracked-pkg"].Kept() {
		t.Fatalf("git-tracked dir must be kept: %+v", byName["tracked-pkg"])
	}
	if byName["node_modules"].Kept() {
		t.Fatalf("untracked dir should be purgeable: %+v", byName["node_modules"])
	}
}
