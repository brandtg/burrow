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
	// A missing root is silent (stock configs ship optional roots that may
	// not exist); only an unreadable root warns.
	if len(warnings) != 0 {
		t.Fatalf("expected no warning for the missing root, got %v", warnings)
	}
}

func TestDiscoverWarnsOnUnreadableRoot(t *testing.T) {
	root := t.TempDir()
	unreadable := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(unreadable, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("running as root: unreadable-dir check is meaningless")
	}
	var warnings []string
	if _, err := Discover(context.Background(), Options{
		Roots: []string{unreadable, root},
		Warn:  func(msg string) { warnings = append(warnings, msg) },
	}); err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for the unreadable root, got %v", warnings)
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

// TestDiscoverSingleAgeWindow pins the v2 policy: one max_age fallback for
// every artifact pattern — no per-type windows, no marker gating. With Trash
// recovery a wrong guess costs disk space, not data.
func TestDiscoverSingleAgeWindow(t *testing.T) {
	root := t.TempDir()
	makeArtifact(t, root, "node_modules", time.Now().Add(-20*24*time.Hour))
	makeArtifact(t, root, "__pycache__", time.Now().Add(-20*24*time.Hour))
	got, err := Discover(context.Background(), Options{
		Roots:        []string{root},
		ArtifactDirs: []string{"node_modules", "__pycache__"},
		MaxAge:       30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Artifact{}
	for _, a := range got {
		byName[filepath.Base(a.Path)] = a
	}
	for _, name := range []string{"node_modules", "__pycache__"} {
		if !byName[name].Kept() {
			t.Fatalf("%s within the 30d window must be kept: %+v", name, byName[name])
		}
	}

	got, err = Discover(context.Background(), Options{
		Roots:        []string{root},
		ArtifactDirs: []string{"node_modules", "__pycache__"},
		MaxAge:       7 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("beyond the window every pattern is a candidate, got %+v", got)
	}
}

// TestDiscoverGenericNameNeedsNoMarker: a generic name (build, dist) is a
// candidate like any other; the ecosystem-marker gating is gone.
func TestDiscoverGenericNameNeedsNoMarker(t *testing.T) {
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
	if len(got) != 1 || got[0].Kept() {
		t.Fatalf("an old untracked build dir must be a candidate: %+v", got)
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
